/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package images

import (
	"context"
	"fmt"
	"time"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/leases"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/core/transfer"
	criconfig "github.com/containerd/containerd/v2/internal/cri/config"
	imagestore "github.com/containerd/containerd/v2/internal/cri/store/image"
	snapshotstore "github.com/containerd/containerd/v2/internal/cri/store/snapshot"
	"github.com/containerd/containerd/v2/internal/cri/util"
	"github.com/containerd/containerd/v2/internal/kmutex"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/errdefs"
	"github.com/containerd/log"
	"github.com/containerd/platforms"
	"golang.org/x/sync/semaphore"

	imagedigest "github.com/opencontainers/go-digest"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

type imageClient interface {
	ListImages(context.Context, ...string) ([]containerd.Image, error)
	GetImage(context.Context, string) (containerd.Image, error)
	Pull(context.Context, string, ...containerd.RemoteOpt) (containerd.Image, error)
}

type ImagePlatform struct {
	Snapshotter string
	Platform    imagespec.Platform
}

type CRIImageService struct {
	runtime.UnimplementedImageServiceServer

	// config contains all image configurations.
	config criconfig.ImageConfig
	// images is the lower level image store used for raw storage,
	// no event publishing should currently be assumed
	images images.Store
	// content is the lower level content store used for raw storage.
	content content.Store
	// client is a subset of the containerd client
	// and will be replaced by image store and transfer service
	client imageClient
	// imageFSPaths contains path to image filesystem for snapshotters.
	imageFSPaths map[string]string
	// runtimePlatforms are the platforms configured for a runtime.
	runtimePlatforms map[string]ImagePlatform
	// imageStore stores all resources associated with images.
	imageStore *imagestore.Store
	// snapshotStore stores information of all snapshots.
	snapshotStore *snapshotstore.Store
	// leases is the lease manager.
	leases leases.Manager
	// transferrer is used to pull image with transfer service
	transferrer transfer.Transferrer
	// unpackDuplicationSuppressor is used to make sure that there is only
	// one in-flight fetch request or unpack handler for a given descriptor's
	// or chain ID.
	unpackDuplicationSuppressor kmutex.KeyedLocker

	// downloadLimiter is used to limit the number of concurrent downloads.
	downloadLimiter *semaphore.Weighted
}

type GRPCCRIImageService struct {
	*CRIImageService
}

type CRIImageServiceOptions struct {
	Content content.Store

	Images images.Store

	ImageFSPaths map[string]string

	RuntimePlatforms map[string]ImagePlatform

	Snapshotters map[string]snapshots.Snapshotter

	Leases leases.Manager

	Client imageClient

	Transferrer transfer.Transferrer
}

// NewService creates a new CRI Image Service
//
// TODO:
//  1. Generalize the image service and merge with a single higher level image service
//  2. Update the options to remove client and imageFSPath
//     - Platform configuration with Array/Map of snapshotter names + filesystem ID + platform matcher + runtime to snapshotter
//     - Transfer service implementation
//     - Image Service (from metadata)
//     - Content store (from metadata)
//  3. Separate image cache and snapshot cache to first class plugins, make the snapshot cache much more efficient and intelligent
func NewService(config criconfig.ImageConfig, options *CRIImageServiceOptions) (*CRIImageService, error) {
	var downloadLimiter *semaphore.Weighted
	if config.MaxConcurrentDownloads > 0 {
		downloadLimiter = semaphore.NewWeighted(int64(config.MaxConcurrentDownloads))
	}
	svc := CRIImageService{
		config:                      config,
		images:                      options.Images,
		content:                     options.Content,
		client:                      options.Client,
		imageStore:                  imagestore.NewStore(options.Images, options.Content, platforms.Default()),
		imageFSPaths:                options.ImageFSPaths,
		runtimePlatforms:            options.RuntimePlatforms,
		snapshotStore:               snapshotstore.NewStore(),
		leases:                      options.Leases,
		transferrer:                 options.Transferrer,
		unpackDuplicationSuppressor: kmutex.New(),
		downloadLimiter:             downloadLimiter,
	}

	log.L.Info("Start snapshots syncer")
	snapshotsSyncer := newSnapshotsSyncer(
		svc.snapshotStore,
		options.Snapshotters,
		time.Duration(svc.config.StatsCollectPeriod)*time.Second,
	)
	snapshotsSyncer.start()

	return &svc, nil
}

// UpdateRuntimeSnapshotter adds or updates the snapshotter mapping for a runtime.
// This is called by the main CRI plugin after both image and runtime plugins are initialized,
// to propagate runtime-specific snapshotters configured in the runtime plugin's config.
func (c *CRIImageService) UpdateRuntimeSnapshotter(runtimeName string, imagePlatform ImagePlatform) {
	if c.runtimePlatforms == nil {
		c.runtimePlatforms = make(map[string]ImagePlatform)
	}
	// Don't override if already configured
	if _, exists := c.runtimePlatforms[runtimeName]; exists {
		log.L.Debugf("Runtime %q already has snapshotter configured, not overriding", runtimeName)
		return
	}
	c.runtimePlatforms[runtimeName] = imagePlatform
	log.L.Infof("Registered runtime %q with snapshotter %q", runtimeName, imagePlatform.Snapshotter)
}

// LocalResolve resolves image reference locally and returns corresponding image metadata. It
// returns errdefs.ErrNotFound if the reference doesn't exist.
func (c *CRIImageService) LocalResolve(refOrID string) (imagestore.Image, error) {
	var imageID string
	if _, err := imagedigest.Parse(refOrID); err == nil {
		imageID = refOrID
	} else {
		// ref is not image id, try to resolve it locally.
		// We use Resolve method of image store which handles normalization.
		id, err := c.imageStore.Resolve(refOrID)
		if err != nil {
			// Not found as name, try to treat ref as imageID
			imageID = refOrID
		} else {
			imageID = id
		}
	}

	img, err := c.imageStore.Get(imageID)
	if err == nil {
		return img, nil
	}
	if !errdefs.IsNotFound(err) {
		return imagestore.Image{}, err
	}

	// Not found in default namespace, search additional namespaces.
	// This "auto-import" logic allows CRI to discover images from other namespaces and
	// "own" them by copying their metadata to the CRI namespace.
	if len(c.config.ImageAutoImportNamespaces) > 0 {
		log.L.Debugf("Image %q not found in CRI namespace, searching additional namespaces: %v", refOrID, c.config.ImageAutoImportNamespaces)
		for _, ns := range c.config.ImageAutoImportNamespaces {
			ctx := namespaces.WithNamespace(context.Background(), ns)
			var otherImg images.Image
			if _, err := imagedigest.Parse(refOrID); err == nil {
				// Search by digest in the source namespace.
				imageList, err := c.images.List(ctx, fmt.Sprintf("target.digest==%s", refOrID))
				if err != nil || len(imageList) == 0 {
					continue
				}
				otherImg = imageList[0]
			} else {
				otherImg, err = c.images.Get(ctx, refOrID)
				if err != nil {
					log.L.Debugf("Image %q not found in namespace %q: %v", refOrID, ns, err)
					continue
				}
			}

			log.L.Infof("Found image %q in additional namespace %q, importing to CRI", refOrID, ns)
			// Found image in another namespace, import it.
			//
			// CONCURRENCY & RACE CONDITION SAFETY:
			// We need to protect against the image's content (blobs/layers) being garbage collected
			// between us finding the image in the source namespace and successfully creating the
			// metadata record in the CRI namespace.
			//
			// 1. WHY WE LEASE:
			//    In containerd, content is kept alive by references (like image records) or leases.
			//    If the image is deleted from the source namespace (`ns`) right after we find it,
			//    and no other references exist, the Garbage Collector (GC) would be free to delete
			//    the blobs.
			//
			// 2. HOW IT INTERACTS WITH OTHER OPERATIONS:
			//    - DELETE: A typical `images.Delete` operation just removes the metadata reference.
			//      It doesn't check for leases on the content it's "releasing". The GC is what
			//      eventually checks all references and leases before deleting content.
			//    - CREATE/UPDATE: These operations add/modify references. `images.Create` in
			//      BoltDB is atomic.
			//    - GC: By creating a lease in the CRI namespace and adding the content digest to it,
			//      we ensure that even if all references in the source namespace are deleted,
			//      the GC will see our active lease and skip these blobs.
			//
			// 3. LIFECYCLE:
			//    Once `c.images.Create` succeeds and the transaction is committed, a permanent
			//    metadata reference now exists in the CRI namespace.
			log.L.Infof("Found image %q in additional namespace %q, importing to CRI", refOrID, ns)
			criCtx := util.NamespacedContext()

			// 1. Create a temporary lease to protect the content from GC during import.
			// This ensures that even if the image is deleted from the source namespace
			// while we are working, the blobs remain available.
			lease, err := c.leases.Create(criCtx, leases.WithRandomID(), leases.WithExpiration(1*time.Minute))
			if err != nil {
				log.L.WithError(err).Warn("failed to create temporary lease for image auto-import, skipping this namespace")
				continue
			}
			defer func() {
				if err := c.leases.Delete(criCtx, lease); err != nil {
					log.L.WithError(err).Warn("failed to delete temporary lease")
				}
			}()

			// 2. Add the image manifest to the lease.
			// This protects the manifest and all its children (layers, config) from GC.
			if err := c.leases.AddResource(criCtx, lease, leases.Resource{
				ID:   otherImg.Target.Digest.String(),
				Type: "content",
			}); err != nil {
				log.L.WithError(err).Warn("failed to add resource to temporary lease, skipping this namespace")
				continue
			}

			// Add metadata indicating the source of the image auto-import.
			if otherImg.Labels == nil {
				otherImg.Labels = make(map[string]string)
			}
			otherImg.Labels["io.containerd.cri.image-auto-import/source-namespace"] = ns
			otherImg.Labels["io.containerd.cri.image-auto-import/imported-at"] = time.Now().UTC().Format(time.RFC3339)

			// 3. Concurrent Imports: If multiple requests try to import the same image simultaneously,
			//    containerd's metadata store (BoltDB) ensures atomicity. One `images.Create` will
			//    succeed, and others will receive `errdefs.ErrAlreadyExists`. We handle this by
			//    falling back to `images.Get`.
			imported, err := c.images.Create(criCtx, otherImg)
			if err != nil {
				if !errdefs.IsAlreadyExists(err) {
					log.L.WithError(err).Warnf("failed to import image %q from namespace %q", otherImg.Name, ns)
					continue
				}
				// If it already exists (e.g., due to a race), just get the existing record.
				imported, err = c.images.Get(criCtx, otherImg.Name)
				if err != nil {
					continue
				}
			}

			// Update the internal in-memory CRI image store to reflect the new local metadata.
			if err := c.imageStore.Update(criCtx, imported.Name); err != nil {
				log.L.WithError(err).Warnf("failed to update image store for imported image %q", imported.Name)
				continue
			}

			id, err := c.imageStore.Resolve(imported.Name)
			if err != nil {
				log.L.WithError(err).Warnf("failed to resolve image id for imported image %q", imported.Name)
				continue
			}

			return c.imageStore.Get(id)
		}
	}

	return imagestore.Image{}, errdefs.ErrNotFound
}

// RuntimeSnapshotter overrides the default snapshotter if Snapshotter is set for this runtime.
// See https://github.com/containerd/containerd/issues/6657
// TODO: Pass in name and get back runtime platform
func (c *CRIImageService) RuntimeSnapshotter(ctx context.Context, ociRuntime criconfig.Runtime) string {
	if ociRuntime.Snapshotter == "" {
		return c.config.Snapshotter
	}

	log.G(ctx).Debugf("Set snapshotter for runtime %s to %s", ociRuntime.Type, ociRuntime.Snapshotter)
	return ociRuntime.Snapshotter
}

// GetImage gets image metadata by image id.
func (c *CRIImageService) GetImage(id string) (imagestore.Image, error) {
	return c.imageStore.Get(id)
}

// GetSnapshot returns the snapshot with specified key.
func (c *CRIImageService) GetSnapshot(key, snapshotter string) (snapshotstore.Snapshot, error) {
	snapshotKey := snapshotstore.Key{
		Key:         key,
		Snapshotter: snapshotter,
	}
	return c.snapshotStore.Get(snapshotKey)
}

func (c *CRIImageService) ImageFSPaths() map[string]string {
	return c.imageFSPaths
}

// Config returns the image configuration.
func (c *CRIImageService) Config() criconfig.ImageConfig {
	return c.config
}

// GRPCService returns a new CRI Image Service grpc server.
func (c *CRIImageService) GRPCService() runtime.ImageServiceServer {
	return &GRPCCRIImageService{c}
}

