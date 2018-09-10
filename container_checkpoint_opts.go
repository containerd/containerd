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

package containerd

import (
	"context"
	"fmt"
	"runtime"

	tasks "github.com/containerd/containerd/api/services/tasks/v1"
	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/diff"
	"github.com/containerd/containerd/rootfs"
	"github.com/containerd/containerd/runtime/v2/runc/options"
	"github.com/containerd/typeurl"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

var (
	// ErrCheckpointRWUnsupported is returned if the container runtime does not support checkpoint
	ErrCheckpointRWUnsupported = errors.New("rw checkpoint is only supported on v2 runtimes")
	// ErrMediaTypeNotFound returns an error when a media type in the manifest is unknown
	ErrMediaTypeNotFound = errors.New("media type not found")
	// ErrCheckpointIndexImageNameNotFound is returned when the checkpoint image name is not present in the index
	ErrCheckpointIndexImageNameNotFound = errors.New("image name not present in index")
	// ErrCheckpointIndexRuntimeNameNotFound is returned when the checkpoint runtime name is not present in the index
	ErrCheckpointIndexRuntimeNameNotFound = errors.New("runtime name not present in index")
)

// CheckpointOpts are options to manage the checkpoint operation
type CheckpointOpts func(context.Context, *Client, *containers.Container, *imagespec.Index, *options.CheckpointOptions) error

// WithCheckpointImage includes the container image in the checkpoint
func WithCheckpointImage(ctx context.Context, client *Client, c *containers.Container, index *imagespec.Index, copts *options.CheckpointOptions) error {
	ir, err := client.ImageService().Get(ctx, c.Image)
	if err != nil {
		return err
	}
	index.Manifests = append(index.Manifests, ir.Target)
	return nil
}

// WithCheckpointTask includes the running task
func WithCheckpointTask(ctx context.Context, client *Client, c *containers.Container, index *imagespec.Index, copts *options.CheckpointOptions) error {
	any, err := typeurl.MarshalAny(copts)
	if err != nil {
		return nil
	}
	task, err := client.TaskService().Checkpoint(ctx, &tasks.CheckpointTaskRequest{
		ContainerID: c.ID,
		Options:     any,
	})
	if err != nil {
		return err
	}
	for _, d := range task.Descriptors {
		index.Manifests = append(index.Manifests, imagespec.Descriptor{
			MediaType: d.MediaType,
			Size:      d.Size_,
			Digest:    d.Digest,
			Platform: &imagespec.Platform{
				OS:           runtime.GOOS,
				Architecture: runtime.GOARCH,
			},
		})
	}
	return nil
}

// WithCheckpointRW includes the rw in the checkpoint
func WithCheckpointRW(ctx context.Context, client *Client, c *containers.Container, index *imagespec.Index, copts *options.CheckpointOptions) error {
	cnt, err := client.LoadContainer(ctx, c.ID)
	if err != nil {
		return err
	}
	info, err := cnt.Info(ctx)
	if err != nil {
		return err
	}

	diffOpts := []diff.Opt{
		diff.WithReference(fmt.Sprintf("checkpoint-rw-%s", info.SnapshotKey)),
	}
	rw, err := rootfs.CreateDiff(ctx,
		info.SnapshotKey,
		client.SnapshotService(info.Snapshotter),
		client.DiffService(),
		diffOpts...,
	)
	if err != nil {
		return err

	}
	rw.Platform = &imagespec.Platform{
		OS:           runtime.GOOS,
		Architecture: runtime.GOARCH,
	}
	index.Manifests = append(index.Manifests, rw)
	return nil
}

// GetIndexByMediaType returns the index in a manifest for the specified media type
func GetIndexByMediaType(index *imagespec.Index, mt string) (*imagespec.Descriptor, error) {
	for _, d := range index.Manifests {
		if d.MediaType == mt {
			return &d, nil
		}
	}
	return nil, ErrMediaTypeNotFound
}
