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
	"maps"
	"slices"
	"time"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/core/transfer"
	criconfig "github.com/containerd/containerd/v2/internal/cri/config"
	imagestore "github.com/containerd/containerd/v2/internal/cri/store/image"
	snapshotstore "github.com/containerd/containerd/v2/internal/cri/store/snapshot"
	"github.com/containerd/containerd/v2/internal/cri/util"
	"github.com/containerd/containerd/v2/internal/kmutex"
	"github.com/containerd/errdefs"
	"github.com/containerd/log"
	"golang.org/x/sync/semaphore"

	docker "github.com/distribution/reference"
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
	// client is a subset of the containerd client
	// and will be replaced by image store and transfer service
	client imageClient
	// imageFSPaths contains path to image filesystem for snapshotters.
	imageFSPaths map[string]string
	// runtimePlatforms are the platforms configured for a runtime.
	runtimePlatforms map[string]ImagePlatform
	// imagePlatforms are the distinct platforms images may be stored for,
	// the platform of the node first, followed by any other platform
	// configured through runtime_platforms.
	imagePlatforms []imagespec.Platform
	// snapshotterProvider resolves a snapshotter by name, so that the
	// presence of an image in the snapshotter of a runtime can be checked
	// without caching state that changes outside of CRI.
	snapshotterProvider func(string) snapshots.Snapshotter
	// imageStore stores all resources associated with images.
	imageStore *imagestore.Store
	// snapshotStore stores information of all snapshots.
	snapshotStore *snapshotstore.Store
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

	// SnapshotterProvider resolves any snapshotter by name. It may be nil,
	// in which case only the snapshotters in Snapshotters are reachable.
	SnapshotterProvider func(string) snapshots.Snapshotter

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
	snapshotterProvider := options.SnapshotterProvider
	if snapshotterProvider == nil {
		snapshotterProvider = func(name string) snapshots.Snapshotter {
			return options.Snapshotters[name]
		}
	}
	svc := CRIImageService{
		config:                      config,
		images:                      options.Images,
		client:                      options.Client,
		imageStore:                  imagestore.NewStore(options.Images, options.Content),
		imageFSPaths:                options.ImageFSPaths,
		runtimePlatforms:            options.RuntimePlatforms,
		imagePlatforms:              imagePlatforms(options.RuntimePlatforms),
		snapshotterProvider:         snapshotterProvider,
		snapshotStore:               snapshotstore.NewStore(),
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

// imagePlatforms returns the distinct platforms images may be stored for.
//
// The platform of the node always comes first, so the common case is
// unaffected. Any other platform configured through runtime_platforms follows,
// so that an image pulled for such a runtime is still found when every stored
// platform has to be visited, as in ListImages and image reload.
func imagePlatforms(runtimePlatforms map[string]ImagePlatform) []imagespec.Platform {
	result := []imagespec.Platform{util.NodePlatform()}
	seen := map[string]struct{}{util.PlatformKey(util.NodePlatform()): {}}
	// Sort by runtime name so the order is deterministic.
	for _, runtimeName := range slices.Sorted(maps.Keys(runtimePlatforms)) {
		p := runtimePlatforms[runtimeName].Platform
		if util.IsNodePlatform(p) {
			continue
		}
		key := util.PlatformKey(p)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, p)
		log.L.Infof("Runtime %q adds image platform %q", runtimeName, key)
	}
	return result
}

// platformsForImages returns the platforms images may be stored for.
func (c *CRIImageService) platformsForImages() []imagespec.Platform {
	if len(c.imagePlatforms) == 0 {
		return []imagespec.Platform{util.NodePlatform()}
	}
	return c.imagePlatforms
}

// PlatformForRuntimeHandler returns the platform images are pulled for on the
// given runtime handler. An unknown or empty handler is the platform of the
// node.
func (c *CRIImageService) PlatformForRuntimeHandler(runtimeHandler string) imagespec.Platform {
	if p, ok := c.runtimePlatforms[runtimeHandler]; ok && !util.IsNodePlatform(p.Platform) {
		return p.Platform
	}
	return util.NodePlatform()
}

// PlatformForImage returns the platform an image is pulled for and looked up on
// for the given runtime handler. Pinned images, the sandbox image in particular,
// are shared by every pod on the node and stay on the platform of the node
// whatever the handler asks for. Every lookup has to agree on this with
// PullImage, otherwise an image that was just pulled for a handler is reported
// absent to that same handler.
func (c *CRIImageService) PlatformForImage(ref, runtimeHandler string) imagespec.Platform {
	if c.isPinnedImage(ref) {
		return util.NodePlatform()
	}
	return c.PlatformForRuntimeHandler(runtimeHandler)
}

// imageConfig resolves the config descriptor of an image against the first of
// the configured image platforms whose content is present locally.
//
// containerd.Image carries the platform matcher of the client, which is always
// the platform of the node, so it cannot be used to resolve an image that was
// pulled for a different platform.
func (c *CRIImageService) imageConfig(ctx context.Context, img containerd.Image) (imagespec.Descriptor, error) {
	provider := img.ContentStore()
	var firstErr error
	for _, platform := range c.platformsForImages() {
		desc, err := images.Config(ctx, provider, img.Target(), util.PlatformMatcher(platform))
		if err == nil {
			// images.Config only requires the manifest of the platform to be
			// present, while the image store also reads the image config.
			// Require it here as well, so that this cannot select a platform
			// that the image store would then reject, which would map the
			// minted image id reference to a different platform.
			if _, err = provider.Info(ctx, desc.Digest); err == nil {
				return desc, nil
			}
		}
		if !errdefs.IsNotFound(err) {
			return imagespec.Descriptor{}, err
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return imagespec.Descriptor{}, firstErr
}

// UpdateRuntimeSnapshotter adds or updates the snapshotter mapping for a runtime.
// This is called by the main CRI plugin after both image and runtime plugins are initialized,
// to propagate runtime-specific snapshotters configured in the runtime plugin's config.
//
// NOTE: only the snapshotter is propagated this way; the platform of the given
// ImagePlatform is expected to be the platform of the node. The set of image
// platforms is fixed when the service is created, so a non-default platform can
// only be configured through the runtime_platforms image config.
func (c *CRIImageService) UpdateRuntimeSnapshotter(runtimeName string, imagePlatform ImagePlatform) {
	if c.runtimePlatforms == nil {
		c.runtimePlatforms = make(map[string]ImagePlatform)
	}
	if existing, exists := c.runtimePlatforms[runtimeName]; exists {
		// Don't override a snapshotter that runtime_platforms configured.
		if existing.Snapshotter != "" {
			log.L.Debugf("Runtime %q already has snapshotter %q configured, not overriding", runtimeName, existing.Snapshotter)
			return
		}
		// The runtime_platforms entry only configured a platform, so the
		// snapshotter of the runtime still applies to it.
		existing.Snapshotter = imagePlatform.Snapshotter
		c.runtimePlatforms[runtimeName] = existing
		log.L.Infof("Registered runtime %q with snapshotter %q", runtimeName, existing.Snapshotter)
		return
	}
	c.runtimePlatforms[runtimeName] = imagePlatform
	log.L.Infof("Registered runtime %q with snapshotter %q", runtimeName, imagePlatform.Snapshotter)
}

// LocalResolve resolves an image reference locally on the given platform and
// returns the corresponding image metadata. An unset platform means the
// platform of the node. It returns errdefs.ErrNotFound if the reference does
// not exist on that platform.
func (c *CRIImageService) LocalResolve(refOrID string, platform imagespec.Platform) (imagestore.Image, error) {
	getImageID := func(refOrId string) string {
		if _, err := imagedigest.Parse(refOrID); err == nil {
			return refOrID
		}
		return func(ref string) string {
			// ref is not image id, try to resolve it locally.
			// TODO(random-liu): Handle this error better for debugging.
			normalized, err := docker.ParseDockerRef(ref)
			if err != nil {
				return ""
			}
			id, err := c.imageStore.Resolve(normalized.String(), platform)
			if err != nil {
				return ""
			}
			return id
		}(refOrID)
	}

	imageID := getImageID(refOrID)
	if imageID == "" {
		// Try to treat ref as imageID
		imageID = refOrID
	}
	return c.imageStore.Get(imageID)
}

// IsImageUnpacked reports whether the image is unpacked in the given
// snapshotter.
//
// The same image can be unpacked in several snapshotters at once, and that
// changes outside of CRI, so it is looked up rather than cached: the chain id
// of the image is the key of its top level snapshot.
func (c *CRIImageService) IsImageUnpacked(ctx context.Context, image imagestore.Image, snapshotter string) (bool, error) {
	if image.ChainID == "" {
		return false, nil
	}
	if snapshotter == "" {
		snapshotter = c.config.Snapshotter
	}
	sn := c.snapshotterProvider(snapshotter)
	if sn == nil {
		return false, fmt.Errorf("snapshotter %q: %w", snapshotter, errdefs.ErrNotFound)
	}
	if _, err := sn.Stat(ctx, image.ChainID); err != nil {
		if errdefs.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("stat snapshot %q in snapshotter %q: %w", image.ChainID, snapshotter, err)
	}
	return true, nil
}

// SnapshotterForRuntimeHandler returns the snapshotter images are unpacked
// into for the given runtime handler.
func (c *CRIImageService) SnapshotterForRuntimeHandler(runtimeHandler string) string {
	if p, ok := c.runtimePlatforms[runtimeHandler]; ok && p.Snapshotter != "" {
		return p.Snapshotter
	}
	return c.config.Snapshotter
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
