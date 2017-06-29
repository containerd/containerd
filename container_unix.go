// +build !windows

package containerd

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"

	"github.com/containerd/containerd/api/services/containers/v1"
	"github.com/containerd/containerd/api/services/tasks/v1"
	"github.com/containerd/containerd/api/types"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/images"
	protobuf "github.com/gogo/protobuf/types"
	digest "github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/identity"
	"github.com/opencontainers/image-spec/specs-go/v1"
	specs "github.com/opencontainers/runtime-spec/specs-go"
)

func WithCheckpoint(desc v1.Descriptor, rootfsID string) NewContainerOpts {
	// set image and rw, and spec
	return func(ctx context.Context, client *Client, c *containers.Container) error {
		id := desc.Digest
		store := client.ContentStore()
		index, err := decodeIndex(ctx, store, id)
		if err != nil {
			return err
		}
		var rw *v1.Descriptor
		for _, m := range index.Manifests {
			switch m.MediaType {
			case v1.MediaTypeImageLayer:
				fk := m
				rw = &fk
			case images.MediaTypeDockerSchema2Manifest:
				config, err := images.Config(ctx, store, m)
				if err != nil {
					return err
				}
				diffIDs, err := images.RootFS(ctx, store, config)
				if err != nil {
					return err
				}
				if _, err := client.SnapshotService().Prepare(ctx, rootfsID, identity.ChainID(diffIDs).String()); err != nil {
					if !errdefs.IsAlreadyExists(err) {
						return err
					}
				}
				c.Image = index.Annotations["image.name"]
			case images.MediaTypeContainerd1CheckpointConfig:
				r, err := store.Reader(ctx, m.Digest)
				if err != nil {
					return err
				}
				data, err := ioutil.ReadAll(r)
				r.Close()
				if err != nil {
					return err
				}
				c.Spec = &protobuf.Any{
					TypeUrl: specs.Version,
					Value:   data,
				}
			}
		}
		if rw != nil {
			// apply the rw snapshot to the new rw layer
			mounts, err := client.SnapshotService().Mounts(ctx, rootfsID)
			if err != nil {
				return err
			}
			if _, err := client.DiffService().Apply(ctx, *rw, mounts); err != nil {
				return err
			}
		}
		c.RootFS = rootfsID
		return nil
	}
}

func WithTaskCheckpoint(desc v1.Descriptor) NewTaskOpts {
	return func(ctx context.Context, c *Client, r *tasks.CreateTaskRequest) error {
		id := desc.Digest
		index, err := decodeIndex(ctx, c.ContentStore(), id)
		if err != nil {
			return err
		}
		for _, m := range index.Manifests {
			if m.MediaType == images.MediaTypeContainerd1Checkpoint {
				r.Checkpoint = &types.Descriptor{
					MediaType: m.MediaType,
					Size_:     m.Size,
					Digest:    m.Digest,
				}
				return nil
			}
		}
		return fmt.Errorf("checkpoint not found in index %s", id)
	}
}

func decodeIndex(ctx context.Context, store content.Store, id digest.Digest) (*v1.Index, error) {
	var index v1.Index
	r, err := store.Reader(ctx, id)
	if err != nil {
		return nil, err
	}
	err = json.NewDecoder(r).Decode(&index)
	r.Close()
	return &index, err
}
