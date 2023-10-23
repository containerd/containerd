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

	"github.com/containerd/log"
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"
	docker "github.com/distribution/reference"
	imagedigest "github.com/opencontainers/go-digest"

	containerd "github.com/containerd/containerd/v2/client"
	criconfig "github.com/containerd/containerd/v2/pkg/cri/config"
	"github.com/containerd/containerd/v2/pkg/cri/constants"
	"github.com/containerd/containerd/v2/pkg/cri/server/base"
	imagestore "github.com/containerd/containerd/v2/pkg/cri/store/image"
	snapshotstore "github.com/containerd/containerd/v2/pkg/cri/store/snapshot"
	ctrdutil "github.com/containerd/containerd/v2/pkg/cri/util"
	"github.com/containerd/containerd/v2/pkg/kmutex"
	"github.com/containerd/containerd/v2/platforms"
	"github.com/containerd/containerd/v2/plugins"
	snapshot "github.com/containerd/containerd/v2/snapshots"
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
			cri := criPlugin.(*base.CRIBase)

			client, err := containerd.New(
				"",
				containerd.WithDefaultNamespace(constants.K8sContainerdNamespace),
				containerd.WithDefaultPlatform(platforms.Default()),
				containerd.WithInMemoryServices(ic),
			)
			if err != nil {
				return nil, fmt.Errorf("unable to init client for cri image service: %w", err)
			}
			service, err := NewService(cri.Config, client)
			if err != nil {
				return nil, fmt.Errorf("failed to create image service: %w", err)
			}

			return service, nil
		},
	})
}

type CRIImageService struct {
	// config contains all configurations.
	config criconfig.Config
	// client is an instance of the containerd client
	client *containerd.Client
	// imageFSPaths contains path to image filesystem for snapshotters.
	imageFSPaths map[string]string
	// imageStore stores all resources associated with images.
	imageStore *imagestore.Store
	// snapshotStore stores information of all snapshots.
	snapshotStore *snapshotstore.Store
	// unpackDuplicationSuppressor is used to make sure that there is only
	// one in-flight fetch request or unpack handler for a given descriptor's
	// or chain ID.
	unpackDuplicationSuppressor kmutex.KeyedLocker
}

func NewService(config criconfig.Config, client *containerd.Client) (*CRIImageService, error) {
	if client.SnapshotService(config.ContainerdConfig.Snapshotter) == nil {
		return nil, fmt.Errorf("failed to find snapshotter %q", config.ContainerdConfig.Snapshotter)
	}

	imageFSPaths := map[string]string{}
	for _, ociRuntime := range config.ContainerdConfig.Runtimes {
		// Can not use `c.RuntimeSnapshotter() yet, so hard-coding here.`
		snapshotter := ociRuntime.Snapshotter
		if snapshotter != "" {
			imageFSPaths[snapshotter] = imageFSPath(config.ContainerdRootDir, snapshotter)
			log.L.Infof("Get image filesystem path %q for snapshotter %q", imageFSPaths[snapshotter], snapshotter)
		}
	}

	snapshotter := config.ContainerdConfig.Snapshotter
	imageFSPaths[snapshotter] = imageFSPath(config.ContainerdRootDir, snapshotter)
	log.L.Infof("Get image filesystem path %q for snapshotter %q", imageFSPaths[snapshotter], snapshotter)

	svc := CRIImageService{
		config:                      config,
		client:                      client,
		imageStore:                  imagestore.NewStore(client.ImageService(), client.ContentStore(), platforms.Default()),
		imageFSPaths:                imageFSPaths,
		snapshotStore:               snapshotstore.NewStore(),
		unpackDuplicationSuppressor: kmutex.New(),
	}

	snapshotters := map[string]snapshot.Snapshotter{}
	ctx := ctrdutil.NamespacedContext()

	// Add runtime specific snapshotters
	for _, runtime := range config.ContainerdConfig.Runtimes {
		snapshotterName := svc.RuntimeSnapshotter(ctx, runtime)
		if snapshotter := svc.client.SnapshotService(snapshotterName); snapshotter != nil {
			snapshotters[snapshotterName] = snapshotter
		} else {
			return nil, fmt.Errorf("failed to find snapshotter %q", snapshotterName)
		}
	}

	// Add default snapshotter
	snapshotterName := svc.config.ContainerdConfig.Snapshotter
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

// imageFSPath returns containerd image filesystem path.
// Note that if containerd changes directory layout, we also needs to change this.
func imageFSPath(rootDir, snapshotter string) string {
	return filepath.Join(rootDir, plugins.SnapshotPlugin.String()+"."+snapshotter)
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
func (c *CRIImageService) RuntimeSnapshotter(ctx context.Context, ociRuntime criconfig.Runtime) string {
	if ociRuntime.Snapshotter == "" {
		return c.config.ContainerdConfig.Snapshotter
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
