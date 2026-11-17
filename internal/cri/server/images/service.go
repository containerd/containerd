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

	"github.com/containerd/errdefs"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/leases"
	"github.com/containerd/containerd/v2/core/metadata"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/core/transfer"
	criconfig "github.com/containerd/containerd/v2/internal/cri/config"
	imagestore "github.com/containerd/containerd/v2/internal/cri/store/image"
	snapshotstore "github.com/containerd/containerd/v2/internal/cri/store/snapshot"
	"github.com/containerd/containerd/v2/internal/kmutex"
	"github.com/containerd/containerd/v2/pkg/gc"
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
	GetImageWithPlatform(context.Context, string, platforms.MatchComparer) (containerd.Image, error)
	ImageFromRecord(images.Image, platforms.MatchComparer) containerd.Image
	Pull(context.Context, string, ...containerd.RemoteOpt) (containerd.Image, error)
	WithLease(context.Context, ...leases.Opt) (context.Context, func(context.Context) error, error)
}

type platformImageClient struct {
	*containerd.Client
}

func (c *platformImageClient) GetImageWithPlatform(ctx context.Context, ref string, platform platforms.MatchComparer) (containerd.Image, error) {
	i, err := c.ImageService().Get(ctx, ref)
	if err != nil {
		return nil, err
	}
	return containerd.NewImageWithPlatform(c.Client, i, platform), nil
}

func (c *platformImageClient) ImageFromRecord(image images.Image, platform platforms.MatchComparer) containerd.Image {
	if platform == nil {
		return containerd.NewImage(c.Client, image)
	}
	return containerd.NewImageWithPlatform(c.Client, image, platform)
}

type ImagePlatform struct {
	Snapshotter string
	// Platform is nil when no platform is explicitly configured.
	Platform *imagespec.Platform
}

type CRIImageService struct {
	runtime.UnimplementedImageServiceServer

	// config contains all image configurations.
	config criconfig.ImageConfig
	// images is the lower level image store used for raw storage,
	// no event publishing should currently be assumed
	images   images.Store
	content  content.Provider
	metadata *metadata.DB
	gc       func(context.Context) (gc.Stats, error)
	// client is a subset of the containerd client
	// and will be replaced by image store and transfer service
	client imageClient
	// imageFSPaths contains path to image filesystem for snapshotters.
	imageFSPaths map[string]string
	// runtimePlatforms maps runtime handlers to their configured image platforms.
	runtimePlatforms map[string]*ImagePlatform
	// defaultRuntimeName is the runtime used when a CRI request has an empty
	// runtime handler.
	defaultRuntimeName string
	// imageStore stores all resources associated with images.
	imageStore *imagestore.Store
	// snapshotStore stores information of all snapshots.
	snapshotStore   *snapshotstore.Store
	snapshotsSyncer *snapshotsSyncer
	// transferrer is used to pull image with transfer service
	transferrer transfer.Transferrer
	// unpackDuplicationSuppressor is used to make sure that there is only
	// one in-flight fetch request or unpack handler for a given descriptor's
	// or chain ID.
	unpackDuplicationSuppressor kmutex.KeyedLocker
	imageMutationLocker         kmutex.KeyedLocker

	// downloadLimiter is used to limit the number of concurrent downloads.
	downloadLimiter *semaphore.Weighted
}

type resolvedRuntimeHandler struct {
	Name        string
	Platform    *imagespec.Platform
	Snapshotter string
}

type GRPCCRIImageService struct {
	*CRIImageService
}

type CRIImageServiceOptions struct {
	Content content.Store

	Images images.Store

	Metadata *metadata.DB

	GC func(context.Context) (gc.Stats, error)

	ImageFSPaths map[string]string

	RuntimePlatforms map[string]*ImagePlatform

	Snapshotters map[string]snapshots.Snapshotter

	Client *containerd.Client

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
		metadata:                    options.Metadata,
		gc:                          options.GC,
		client:                      &platformImageClient{Client: options.Client},
		imageStore:                  imagestore.NewStore(options.Images, options.Content, platforms.Default()),
		imageFSPaths:                options.ImageFSPaths,
		runtimePlatforms:            options.RuntimePlatforms,
		snapshotStore:               snapshotstore.NewStore(),
		transferrer:                 options.Transferrer,
		unpackDuplicationSuppressor: kmutex.New(),
		imageMutationLocker:         kmutex.New(),
		downloadLimiter:             downloadLimiter,
	}

	log.L.Info("Start snapshots syncer")
	svc.snapshotsSyncer = newSnapshotsSyncer(
		svc.snapshotStore,
		options.Snapshotters,
		time.Duration(svc.config.StatsCollectPeriod)*time.Second,
	)
	svc.snapshotsSyncer.enable(config.Snapshotter)
	for _, p := range svc.runtimePlatforms {
		if p != nil {
			svc.snapshotsSyncer.enable(p.Snapshotter)
		}
	}
	svc.snapshotsSyncer.start()

	return &svc, nil
}

// UpdateRuntime records a runtime handler and its image settings.
func (c *CRIImageService) UpdateRuntime(runtimeName string, imagePlatform *ImagePlatform) {
	if c.runtimePlatforms == nil {
		c.runtimePlatforms = make(map[string]*ImagePlatform)
	}
	// Fill missing settings without overriding image configuration.
	if existing, exists := c.runtimePlatforms[runtimeName]; exists {
		if existing != nil && existing.Snapshotter == "" && imagePlatform != nil {
			existing.Snapshotter = imagePlatform.Snapshotter
		}
		log.L.Debugf("Runtime %q already configured, preserving image settings", runtimeName)
	} else {
		c.runtimePlatforms[runtimeName] = imagePlatform
		if imagePlatform != nil {
			log.L.Infof("Registered runtime %q with snapshotter %q", runtimeName, imagePlatform.Snapshotter)
		}
	}
	if c.snapshotsSyncer != nil {
		if imagePlatform != nil {
			c.snapshotsSyncer.enable(imagePlatform.Snapshotter)
		}
		if p := c.runtimePlatforms[runtimeName]; p != nil {
			c.snapshotsSyncer.enable(p.Snapshotter)
		}
	}
}

// UpdateDefaultRuntimeName records the runtime used for CRI requests with an empty
// runtime handler.
func (c *CRIImageService) UpdateDefaultRuntimeName(runtimeName string) {
	c.defaultRuntimeName = runtimeName
}

func (c *CRIImageService) resolveRuntimeHandler(handler string) (resolvedRuntimeHandler, error) {
	if handler != "" {
		if _, ok := c.runtimePlatforms[handler]; !ok {
			return resolvedRuntimeHandler{}, fmt.Errorf("unknown runtime handler %q: %w", handler, errdefs.ErrInvalidArgument)
		}
	}

	name := handler
	if name == "" {
		name = c.defaultRuntimeName
	}
	h := resolvedRuntimeHandler{
		Name:        name,
		Snapshotter: c.config.Snapshotter,
	}
	if p, ok := c.runtimePlatforms[name]; ok && p != nil {
		h.Platform = p.Platform
		if p.Snapshotter != "" {
			h.Snapshotter = p.Snapshotter
		}
	}
	return h, nil
}

// LocalResolve resolves image reference locally and returns corresponding image metadata. It
// returns errdefs.ErrNotFound if the reference doesn't exist.
func (c *CRIImageService) LocalResolve(refOrID, runtimeHandler string) (imagestore.Image, error) {
	if runtimeHandler == "" {
		runtimeHandler = c.defaultRuntimeName
	}
	id := refOrID
	if _, err := imagedigest.Parse(refOrID); err != nil {
		if normalized, err := docker.ParseDockerRef(refOrID); err == nil {
			id, err = c.imageStore.ResolveForRuntime(normalized.String(), runtimeHandler)
			if err != nil && errdefs.IsNotFound(err) && runtimeHandler == c.defaultRuntimeName {
				id, err = c.imageStore.Resolve(normalized.String())
			}
			if err != nil {
				id = refOrID
			}
		}
	}
	return c.GetImage(id, runtimeHandler)
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
func (c *CRIImageService) GetImage(id, runtimeHandler string) (imagestore.Image, error) {
	if runtimeHandler == "" {
		runtimeHandler = c.defaultRuntimeName
	}
	image, err := c.imageStore.GetForRuntime(id, runtimeHandler)
	if runtimeHandler == "" || runtimeHandler != c.defaultRuntimeName || (err != nil && !errdefs.IsNotFound(err)) {
		return image, err
	}
	legacyID := id
	if err == nil {
		legacyID = image.ID
	}
	legacy, legacyErr := c.imageStore.Get(legacyID)
	if err != nil {
		if legacyErr != nil {
			return image, err
		}
		image = legacy
	} else if legacyErr == nil {
		image.References = append(append([]string{}, image.References...), legacy.References...)
		image.Pinned = image.Pinned || legacy.Pinned
	}
	image.RuntimeHandler = runtimeHandler
	return image, nil
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
