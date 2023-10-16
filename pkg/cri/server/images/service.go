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

	"github.com/containerd/containerd"
	criconfig "github.com/containerd/containerd/pkg/cri/config"
	imagestore "github.com/containerd/containerd/pkg/cri/store/image"
	snapshotstore "github.com/containerd/containerd/pkg/cri/store/snapshot"
	ctrdutil "github.com/containerd/containerd/pkg/cri/util"
	"github.com/containerd/containerd/pkg/kmutex"
	"github.com/containerd/containerd/platforms"
	snapshot "github.com/containerd/containerd/snapshots"
	"github.com/containerd/log"
	docker "github.com/distribution/reference"
	imagedigest "github.com/opencontainers/go-digest"
)

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

func NewService(config criconfig.Config, imageFSPaths map[string]string, client *containerd.Client) (*CRIImageService, error) {
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
