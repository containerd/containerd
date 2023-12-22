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
	"path/filepath"
	"time"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/content"
	"github.com/containerd/containerd/v2/events"
	"github.com/containerd/containerd/v2/images"
	"github.com/containerd/containerd/v2/metadata"
	criconfig "github.com/containerd/containerd/v2/pkg/cri/config"
	"github.com/containerd/containerd/v2/pkg/cri/constants"
	"github.com/containerd/containerd/v2/pkg/cri/server/base"
	imagestore "github.com/containerd/containerd/v2/pkg/cri/store/image"
	snapshotstore "github.com/containerd/containerd/v2/pkg/cri/store/snapshot"
	"github.com/containerd/containerd/v2/pkg/kmutex"
	"github.com/containerd/containerd/v2/platforms"
	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/containerd/v2/snapshots"
	"github.com/containerd/log"
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"
	docker "github.com/distribution/reference"
	imagedigest "github.com/opencontainers/go-digest"

	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func init() {
	registry.Register(&plugin.Registration{
		Type: plugins.CRIImagePlugin,
		ID:   "cri-image-service",
		Requires: []plugin.Type{
			plugins.LeasePlugin,
			plugins.EventPlugin,
			plugins.MetadataPlugin,
			plugins.SandboxStorePlugin,
			plugins.InternalPlugin,
			plugins.ServicePlugin,
			plugins.SnapshotPlugin,
		},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			// Get base CRI dependencies.
			criPlugin, err := ic.GetByID(plugins.InternalPlugin, "cri")
			if err != nil {
				return nil, fmt.Errorf("unable to load CRI service base dependencies: %w", err)
			}
			// TODO: Move this to migration directly
			c := criPlugin.(*base.CRIBase).Config

			m, err := ic.GetSingle(plugins.MetadataPlugin)
			if err != nil {
				return nil, err
			}
			mdb := m.(*metadata.DB)

			ep, err := ic.GetSingle(plugins.EventPlugin)
			if err != nil {
				return nil, err
			}

			options := &CRIImageServiceOptions{
				Content:          mdb.ContentStore(),
				Images:           metadata.NewImageStore(mdb),
				RuntimePlatforms: map[string]ImagePlatform{},
				Snapshotters:     map[string]snapshots.Snapshotter{},
				ImageFSPaths:     map[string]string{},
				Publisher:        ep.(events.Publisher),
			}

			options.Client, err = containerd.New(
				"",
				containerd.WithDefaultNamespace(constants.K8sContainerdNamespace),
				containerd.WithDefaultPlatform(platforms.Default()),
				containerd.WithInMemoryServices(ic),
			)
			if err != nil {
				return nil, fmt.Errorf("unable to init client for cri image service: %w", err)
			}

			allSnapshotters := mdb.Snapshotters()
			defaultSnapshotter := c.ImageConfig.Snapshotter
			if s, ok := allSnapshotters[defaultSnapshotter]; ok {
				options.Snapshotters[defaultSnapshotter] = s
			} else {
				return nil, fmt.Errorf("failed to find snapshotter %q", defaultSnapshotter)
			}
			var snapshotRoot string
			if plugin := ic.Plugins().Get(plugins.SnapshotPlugin, defaultSnapshotter); plugin != nil {
				snapshotRoot = plugin.Meta.Exports["root"]
			}
			if snapshotRoot == "" {
				// Try a root in the same parent as this plugin
				snapshotRoot = filepath.Join(filepath.Dir(ic.Properties[plugins.PropertyRootDir]), plugins.SnapshotPlugin.String()+"."+defaultSnapshotter)
			}
			options.ImageFSPaths[defaultSnapshotter] = snapshotRoot
			log.L.Infof("Get image filesystem path %q for snapshotter %q", snapshotRoot, defaultSnapshotter)

			for runtimeName, rp := range c.ImageConfig.RuntimePlatforms {
				// Can not use `c.RuntimeSnapshotter() yet, so hard-coding here.`
				snapshotter := rp.Snapshotter
				if snapshotter == "" {
					snapshotter = defaultSnapshotter
				} else if _, ok := options.ImageFSPaths[snapshotter]; !ok {
					if s, ok := options.Snapshotters[defaultSnapshotter]; ok {
						options.Snapshotters[defaultSnapshotter] = s
					} else {
						return nil, fmt.Errorf("failed to find snapshotter %q", defaultSnapshotter)
					}
					var snapshotRoot string
					if plugin := ic.Plugins().Get(plugins.SnapshotPlugin, snapshotter); plugin != nil {
						snapshotRoot = plugin.Meta.Exports["root"]
					}
					if snapshotRoot == "" {
						// Try a root in the same parent as this plugin
						snapshotRoot = filepath.Join(filepath.Dir(ic.Properties[plugins.PropertyRootDir]), plugins.SnapshotPlugin.String()+"."+snapshotter)
					}

					options.ImageFSPaths[defaultSnapshotter] = snapshotRoot
					log.L.Infof("Get image filesystem path %q for snapshotter %q", options.ImageFSPaths[snapshotter], snapshotter)
				}
				platform := platforms.DefaultSpec()
				if rp.Platform != "" {
					p, err := platforms.Parse(rp.Platform)
					if err != nil {
						return nil, fmt.Errorf("unable to parse platform %q: %w", rp.Platform, err)
					}
					platform = p
				}
				options.RuntimePlatforms[runtimeName] = ImagePlatform{
					Snapshotter: snapshotter,
					Platform:    platform,
				}
			}

			service, err := NewService(c.ImageConfig, options)
			if err != nil {
				return nil, fmt.Errorf("failed to create image service: %w", err)
			}

			return service, nil
		},
	})
}

type imageClient interface {
	ListImages(context.Context, ...string) ([]containerd.Image, error)
	GetImage(context.Context, string) (containerd.Image, error)
	Pull(context.Context, string, ...containerd.RemoteOpt) (containerd.Image, error)
}

type ImagePlatform struct {
	Snapshotter string
	Platform    platforms.Platform
}

type CRIImageService struct {
	// config contains all configurations.
	// TODO: Migrate configs from cri type once moved to its own plugin
	// - snapshotter
	// - runtime snapshotter
	// - Discard unpack layers
	// - Disable snapshot annotations
	// - Max concurrent downloads (moved to transfer service)
	// - Pull progress timeout
	// - Registry headers (moved to transfer service)
	// - mirror (moved to transfer service)
	// - image decryption (moved to transfer service)
	// - default runtime
	// - stats collection interval (only used to startup snapshot sync)
	config criconfig.ImageConfig
	// images is the lower level image store used for raw storage,
	// no event publishing should currently be assumed
	images images.Store
	// publisher is the events publisher
	publisher events.Publisher
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
	// unpackDuplicationSuppressor is used to make sure that there is only
	// one in-flight fetch request or unpack handler for a given descriptor's
	// or chain ID.
	unpackDuplicationSuppressor kmutex.KeyedLocker
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

	Publisher events.Publisher

	Client imageClient
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
	svc := CRIImageService{
		config:                      config,
		images:                      options.Images,
		client:                      options.Client,
		imageStore:                  imagestore.NewStore(options.Images, options.Content, platforms.Default()),
		imageFSPaths:                options.ImageFSPaths,
		runtimePlatforms:            options.RuntimePlatforms,
		snapshotStore:               snapshotstore.NewStore(),
		unpackDuplicationSuppressor: kmutex.New(),
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

// NewGRPCService creates a new CRI Image Service grpc server.
func NewGRPCService(imageService *CRIImageService) runtime.ImageServiceServer {
	return &GRPCCRIImageService{imageService}
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
