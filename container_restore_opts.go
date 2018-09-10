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

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/oci"
	"github.com/containerd/typeurl"
	"github.com/gogo/protobuf/proto"
	ptypes "github.com/gogo/protobuf/types"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

// RestoreOpts are options to manage the restore operation
type RestoreOpts func(context.Context, *Client, Image, *imagespec.Index) ([]NewContainerOpts, []NewTaskOpts, error)

// WithRestoreLive restores the runtime and memory data for the container
func WithRestoreLive(ctx context.Context, client *Client, checkpoint Image, index *imagespec.Index) ([]NewContainerOpts, []NewTaskOpts, error) {
	return nil, []NewTaskOpts{
		WithTaskCheckpoint(checkpoint),
	}, nil
}

// WithRestoreRuntime restores the runtime for the container
func WithRestoreRuntime(ctx context.Context, client *Client, checkpoint Image, index *imagespec.Index) ([]NewContainerOpts, []NewTaskOpts, error) {
	runtimeName, ok := index.Annotations["runtime.name"]
	if !ok {
		return nil, nil, ErrCheckpointIndexRuntimeNameNotFound
	}
	// restore options if present
	m, err := GetIndexByMediaType(index, images.MediaTypeContainerd1CheckpointRuntimeOptions)
	if err != nil {
		if err != ErrMediaTypeNotFound {
			return nil, nil, err
		}
	}
	var options *ptypes.Any
	if m != nil {
		store := client.ContentStore()
		data, err := content.ReadBlob(ctx, store, *m)
		if err != nil {
			return nil, nil, errors.Wrap(err, "unable to read checkpoint runtime")
		}
		if err := proto.Unmarshal(data, options); err != nil {
			return nil, nil, err
		}
	}
	return []NewContainerOpts{
		WithRuntime(runtimeName, options),
	}, nil, nil
}

// WithRestoreSpec restores the spec from the checkpoint for the container
func WithRestoreSpec(ctx context.Context, client *Client, checkpoint Image, index *imagespec.Index) ([]NewContainerOpts, []NewTaskOpts, error) {
	m, err := GetIndexByMediaType(index, images.MediaTypeContainerd1CheckpointConfig)
	if err != nil {
		return nil, nil, err
	}
	store := client.ContentStore()
	data, err := content.ReadBlob(ctx, store, *m)
	if err != nil {
		return nil, nil, errors.Wrap(err, "unable to read checkpoint config")
	}
	var any ptypes.Any
	if err := proto.Unmarshal(data, &any); err != nil {
		return nil, nil, err
	}

	v, err := typeurl.UnmarshalAny(&any)
	if err != nil {
		return nil, nil, err
	}
	spec := v.(*oci.Spec)
	return []NewContainerOpts{
		WithSpec(spec),
	}, nil, nil
}
