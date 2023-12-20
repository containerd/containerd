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
	criconfig "github.com/containerd/containerd/v2/pkg/cri/config"
	"github.com/containerd/containerd/v2/pkg/cri/constants"
	"github.com/containerd/containerd/v2/pkg/cri/server/base"
	imagestore "github.com/containerd/containerd/v2/pkg/cri/store/image"
	snapshotstore "github.com/containerd/containerd/v2/pkg/cri/store/snapshot"
	"github.com/containerd/containerd/v2/pkg/kmutex"
	"github.com/containerd/containerd/v2/platforms"
	"github.com/containerd/containerd/v2/plugins"
	snapshot "github.com/containerd/containerd/v2/snapshots"
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
			plugins.SandboxStorePlugin,
			plugins.InternalPlugin,
			plugins.ServicePlugin,
		},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			// Get base CRI dependencies.
			criPlugin, err := ic.GetByID(plugins.InternalPlugin, "cri")
			if err != nil {
				return nil, fmt.Errorf("unable to load CRI service base dependencies: %w", err)
			}
			c := criPlugin.(*base.CRIBase).Config

			client, err := containerd.New(
				"",
				containerd.WithDefaultNamespace(constants.K8sContainerdNamespace),
				containerd.WithDefaultPlatform(platforms.Default()),
				containerd.WithInMemoryServices(ic),
			)
			if err != nil {
				return nil, fmt.Errorf("unable to init client for cri image service: %w", err)
			}

			snapshotterOverrides := map[string]RuntimePlatform{}
			imageFSPaths := map[string]string{}
			// TODO: Figure out a way to break this plugin's dependency on a shared runtime config
			for runtimeName, ociRuntime := range c.ContainerdConfig.Runtimes {
				// Can not use `c.RuntimeSnapshotter() yet, so hard-coding here.`
				snapshotter := ociRuntime.Snapshotter
				if snapshotter != "" {
					snapshotterOverrides[runtimeName] = RuntimePlatform{
						Snapshotter: snapshotter,
						// TODO: This must be provided by runtime
						Platform: platforms.DefaultSpec(),
					}
					imageFSPaths[snapshotter] = filepath.Join(c.ContainerdRootDir, plugins.SnapshotPlugin.String()+"."+snapshotter)
					log.L.Infof("Get image filesystem path %q for snapshotter %q", imageFSPaths[snapshotter], snapshotter)
				}
			}
			snapshotter := c.ImageConfig.Snapshotter
			imageFSPaths[snapshotter] = filepath.Join(c.ContainerdRootDir, plugins.SnapshotPlugin.String()+"."+snapshotter)
			log.L.Infof("Get image filesystem path %q for snapshotter %q", imageFSPaths[snapshotter], snapshotter)

			// TODO: Pull out image specific configs here!
			service, err := NewService(c.ImageConfig, imageFSPaths, snapshotterOverrides, client)
			if err != nil {
				return nil, fmt.Errorf("failed to create image service: %w", err)
			}

			return service, nil
		},
	})
}

type RuntimePlatform struct {
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
	// client is an instance of the containerd client
	// TODO: Remove this in favor of using plugins directly
	client *containerd.Client
	// imageFSPaths contains path to image filesystem for snapshotters.
	imageFSPaths map[string]string
	// runtimePlatforms are the platforms configured for a runtime.
	runtimePlatforms map[string]RuntimePlatform
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
func NewService(config criconfig.ImageConfig, imageFSPaths map[string]string, runtimePlatforms map[string]RuntimePlatform, client *containerd.Client) (*CRIImageService, error) {
	svc := CRIImageService{
		config:                      config,
		client:                      client,
		imageStore:                  imagestore.NewStore(client.ImageService(), client.ContentStore(), platforms.Default()),
		imageFSPaths:                imageFSPaths,
		runtimePlatforms:            runtimePlatforms,
		snapshotStore:               snapshotstore.NewStore(),
		unpackDuplicationSuppressor: kmutex.New(),
	}

	snapshotters := map[string]snapshot.Snapshotter{}

	for _, rp := range runtimePlatforms {
		if snapshotter := svc.client.SnapshotService(rp.Snapshotter); snapshotter != nil {
			snapshotters[rp.Snapshotter] = snapshotter
		} else {
			return nil, fmt.Errorf("failed to find snapshotter %q", rp.Snapshotter)
		}
	}

	// Add default snapshotter
	snapshotterName := svc.config.Snapshotter
	if snapshotter := svc.client.SnapshotService(snapshotterName); snapshotter != nil {
		snapshotters[snapshotterName] = snapshotter
	} else {
		return nil, fmt.Errorf("failed to find snapshotter %q", snapshotterName)
	}

	// Start snapshot stats syncer, it doesn't need to be stopped.
	log.L.Info("Start snapshots syncer")
	snapshotsSyncer := newSnapshotsSyncer(
		svc.snapshotStore,
		snapshotters,
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
