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
	"strings"
	"time"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/core/transfer"
	criconfig "github.com/containerd/containerd/v2/internal/cri/config"
	imagestore "github.com/containerd/containerd/v2/internal/cri/store/image"
	snapshotstore "github.com/containerd/containerd/v2/internal/cri/store/snapshot"
	"github.com/containerd/containerd/v2/internal/kmutex"
	snpkg "github.com/containerd/containerd/v2/pkg/snapshotters"
	"github.com/containerd/log"
	"github.com/containerd/platforms"
	docker "github.com/distribution/reference"
	imagedigest "github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/identity"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/semaphore"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

type imageClient interface {
	ListImages(context.Context, ...string) ([]containerd.Image, error)
	GetImage(context.Context, string) (containerd.Image, error)
	Pull(context.Context, string, ...containerd.RemoteOpt) (containerd.Image, error)
	SnapshotService(snapshotterName string) snapshots.Snapshotter
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
	// imageStore stores all resources associated with images.
	imageStore *imagestore.Store
	// snapshotStore stores information of all snapshots.
	snapshotStore *snapshotstore.Store
	// snapshotters provides access to snapshotter instances.
	snapshotters map[string]snapshots.Snapshotter
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
		client:                      options.Client,
		imageStore:                  imagestore.NewStore(options.Images, options.Content, platforms.Default()),
		imageFSPaths:                options.ImageFSPaths,
		runtimePlatforms:            options.RuntimePlatforms,
		snapshotStore:               snapshotstore.NewStore(),
		snapshotters:                options.Snapshotters,
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

// LocalResolve resolves image reference locally and returns corresponding image metadata. It
// returns errdefs.ErrNotFound if the reference doesn't exist.
func (c *CRIImageService) LocalResolve(refOrID string) (imagestore.Image, error) {
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
			id, err := c.imageStore.Resolve(normalized.String())
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

// IsImageUnpackedForSnapshotter checks if an image is unpacked for the given snapshotter.
// When using a runtime-specific snapshotter (different from the default),
// this also verifies that the required labels are present on the snapshots.
// If labels are missing (e.g., from images pulled before the label fix),
// returns false to trigger a re-pull with proper labels.
func (c *CRIImageService) IsImageUnpackedForSnapshotter(ctx context.Context, ref string, snapshotter string) (bool, error) {
	image, err := c.client.GetImage(ctx, ref)
	if err != nil {
		return false, err
	}

	unpacked, err := image.IsUnpacked(ctx, snapshotter)
	if err != nil || !unpacked {
		return unpacked, err
	}

	// When using a runtime-specific snapshotter (not the default),
	// verify that snapshots have required labels. This ensures that images
	// pulled before the runtime-snapshotter fix get re-pulled with proper labels.
	defaultSnapshotter := c.config.Snapshotter
	if snapshotter != defaultSnapshotter {
		hasLabels, err := c.verifySnapshotLabels(ctx, image, snapshotter)
		if err != nil {
			log.G(ctx).WithError(err).Warnf("Failed to verify snapshot labels for %s", ref)
			// If we can't verify, assume it's OK to avoid breaking existing setups
			return true, nil
		}
		if !hasLabels {
			log.G(ctx).Debugf("Image %s unpacked for %s but missing required labels", ref, snapshotter)
			return false, nil
		}
	}

	return true, nil
}

// UnpackImage unpacks an existing image into the specified snapshotter.
// This is used when an image exists locally but needs to be unpacked for a different
// snapshotter (e.g., for remote/proxy snapshotters like nydus).
// Unlike PullImage, this does not contact the registry.
//
// The ref parameter can be any image reference (tag, digest, or image ID).
// For remote snapshotters that need a pullable reference in their metadata,
// this function resolves the ref to a pullable reference automatically.
//
// If snapshots already exist with incorrect labels (e.g., from a previous unpack
// with wrong configuration), they are updated with the correct labels.
func (c *CRIImageService) UnpackImage(ctx context.Context, ref string, snapshotter string) error {
	// Resolve ref to a pullable reference for remote snapshotters.
	// Remote snapshotters like nydus need a full image reference (not just a digest)
	// to pull the image content inside the guest VM.
	pullableRef := ref
	if img, err := c.LocalResolve(ref); err == nil && len(img.References) > 0 {
		// Prefer a tag reference over a digest reference
		for _, imgRef := range img.References {
			if !strings.Contains(imgRef, "@sha256:") {
				pullableRef = imgRef
				break
			}
		}
		// Fallback to first reference if all are digest-based
		if pullableRef == ref && len(img.References) > 0 {
			pullableRef = img.References[0]
		}
	}

	image, err := c.client.GetImage(ctx, pullableRef)
	if err != nil {
		// If pullableRef doesn't work, try with original ref
		image, err = c.client.GetImage(ctx, ref)
		if err != nil {
			return err
		}
		pullableRef = ref
	}

	// Fix existing snapshot labels if they have wrong values.
	// This handles the case where image was previously unpacked for this snapshotter
	// but with incorrect labels (e.g., digest instead of pullable reference).
	if err := c.fixSnapshotLabels(ctx, image, snapshotter, pullableRef); err != nil {
		log.G(ctx).WithError(err).Warnf("Failed to fix snapshot labels for %s, continuing anyway", pullableRef)
	}

	// Pass labels required by remote snapshotters.
	// These labels are normally set during PullImage via AppendInfoHandlerWrapper,
	// but when unpacking an existing image for a different snapshotter, we need
	// to pass them explicitly.
	labels := map[string]string{
		snpkg.TargetRefLabel: pullableRef,
	}

	log.G(ctx).Debugf("Unpacking image %q (resolved to %q) for snapshotter %q", ref, pullableRef, snapshotter)
	return image.Unpack(ctx, snapshotter, containerd.WithUnpackSnapshotOpts(snapshots.WithLabels(labels)))
}

// fixSnapshotLabels updates snapshot labels if they have incorrect values.
// This is needed when an image was previously unpacked with wrong metadata
// (e.g., digest instead of pullable reference) and needs the correct label
// for remote snapshotters like nydus.
func (c *CRIImageService) fixSnapshotLabels(ctx context.Context, image containerd.Image, snapshotter string, expectedRef string) error {
	ss := c.client.SnapshotService(snapshotter)

	// Get image layer chain IDs
	diffIDs, err := image.RootFS(ctx)
	if err != nil {
		return err
	}
	if len(diffIDs) == 0 {
		return nil
	}

	// Check and fix each layer's snapshot labels
	for i := range diffIDs {
		chainID := identity.ChainID(diffIDs[:i+1]).String()

		info, err := ss.Stat(ctx, chainID)
		if err != nil {
			// Snapshot doesn't exist, that's fine
			continue
		}

		// Check if the label value is wrong (digest instead of pullable reference)
		if labelVal, ok := info.Labels[snpkg.TargetRefLabel]; ok {
			if strings.HasPrefix(labelVal, "sha256:") && labelVal != expectedRef {
				// Update the label with the correct value
				info.Labels[snpkg.TargetRefLabel] = expectedRef
				if _, err := ss.Update(ctx, info, "labels."+snpkg.TargetRefLabel); err != nil {
					log.G(ctx).WithError(err).Warnf("Failed to update label on snapshot %s", chainID)
				}
			}
		} else {
			// Label doesn't exist, add it
			if info.Labels == nil {
				info.Labels = make(map[string]string)
			}
			info.Labels[snpkg.TargetRefLabel] = expectedRef
			if _, err := ss.Update(ctx, info, "labels."+snpkg.TargetRefLabel); err != nil {
				log.G(ctx).WithError(err).Warnf("Failed to add label to snapshot %s", chainID)
			}
		}
	}

	return nil
}

// verifySnapshotLabels checks if the image's snapshots have the required labels
// for runtime-specific snapshotters.
func (c *CRIImageService) verifySnapshotLabels(ctx context.Context, image containerd.Image, snapshotter string) (bool, error) {
	ss := c.client.SnapshotService(snapshotter)

	// Get the image's root filesystem descriptor to find the top layer
	diffIDs, err := image.RootFS(ctx)
	if err != nil {
		return false, err
	}
	if len(diffIDs) == 0 {
		return true, nil // No layers, nothing to check
	}

	// The snapshot key for the top layer is the ChainID
	chainID := identity.ChainID(diffIDs).String()

	// Check if the snapshot has the TargetRefLabel
	info, err := ss.Stat(ctx, chainID)
	if err != nil {
		return false, err
	}

	// Check for the required label with a pullable reference (not just a digest).
	// A pullable reference should NOT start with "sha256:" - that's just a digest
	// and can't be used by remote snapshotters to pull the image.
	if ref, ok := info.Labels[snpkg.TargetRefLabel]; ok {
		if !strings.HasPrefix(ref, "sha256:") {
			return true, nil
		}
	}

	return false, nil
}
