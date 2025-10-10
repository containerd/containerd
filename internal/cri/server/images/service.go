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
	"github.com/containerd/log"
	"github.com/containerd/platforms"
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

// PinnedImage is used to lookup a pinned image by name.
// Most often used to get the "sandbox" image.
func (c *CRIImageService) PinnedImage(name string) string {
	return c.config.PinnedImages[name]
}

// GRPCService returns a new CRI Image Service grpc server.
func (c *CRIImageService) GRPCService() runtime.ImageServiceServer {
	return &GRPCCRIImageService{c}
}
