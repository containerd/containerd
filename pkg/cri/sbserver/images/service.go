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

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/log"
	criconfig "github.com/containerd/containerd/pkg/cri/config"
	"github.com/containerd/containerd/pkg/cri/sbserver/base"
	imagestore "github.com/containerd/containerd/pkg/cri/store/image"
	snapshotstore "github.com/containerd/containerd/pkg/cri/store/snapshot"
	"github.com/containerd/containerd/pkg/kmutex"
	"github.com/containerd/containerd/plugin"
	docker "github.com/distribution/reference"
	imagedigest "github.com/opencontainers/go-digest"
)

func init() {
	plugin.Register(&plugin.Registration{
		Type: plugin.GRPCPlugin,
		ID:   "cri-image-service",
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			// Get base CRI dependencies.
			criPlugin, err := ic.GetByID(plugin.GRPCPlugin, "cri")
			if err != nil {
				return nil, fmt.Errorf("unable to load CRI service base dependencies: %w", err)
			}

			cri := criPlugin.(*base.CRIBase)

			service, err := NewService(cri.Config, cri.Client)
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
	// imageFSPath is the path to image filesystem.
	imageFSPath string
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

	imageFSPath := imageFSPath(config.ContainerdRootDir, config.ContainerdConfig.Snapshotter)
	log.L.Infof("Get image filesystem path %q", imageFSPath)

	svc := CRIImageService{
		config:                      config,
		client:                      client,
		imageStore:                  imagestore.NewStore(client),
		imageFSPath:                 imageFSPath,
		snapshotStore:               snapshotstore.NewStore(),
		unpackDuplicationSuppressor: kmutex.New(),
	}

	// Start snapshot stats syncer, it doesn't need to be stopped.
	log.L.Info("Start snapshots syncer")
	snapshotsSyncer := newSnapshotsSyncer(
		svc.snapshotStore,
		svc.client.SnapshotService(svc.config.ContainerdConfig.Snapshotter),
		time.Duration(svc.config.StatsCollectPeriod)*time.Second,
	)
	snapshotsSyncer.start()

	return &svc, nil
}

// imageFSPath returns containerd image filesystem path.
// Note that if containerd changes directory layout, we also needs to change this.
func imageFSPath(rootDir, snapshotter string) string {
	return filepath.Join(rootDir, plugin.SnapshotPlugin.String()+"."+snapshotter)
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
func (c *CRIImageService) GetSnapshot(key string) (snapshotstore.Snapshot, error) {
	return c.snapshotStore.Get(key)
}

func (c *CRIImageService) ImageFSPath() string {
	return c.imageFSPath
}
