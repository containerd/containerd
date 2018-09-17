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
	"github.com/containerd/containerd/platforms"
	"github.com/containerd/typeurl"
	"github.com/gogo/protobuf/proto"
	ptypes "github.com/gogo/protobuf/types"
	"github.com/opencontainers/image-spec/identity"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

// RestoreOpts are options to manage the restore operation
type RestoreOpts func(context.Context, string, *Client, Image, *imagespec.Index) ([]NewContainerOpts, error)

// WithRestoreImage restores the image for the container
func WithRestoreImage(ctx context.Context, id string, client *Client, checkpoint Image, index *imagespec.Index) ([]NewContainerOpts, error) {
	store := client.ContentStore()
	m, err := GetIndexByMediaType(index, images.MediaTypeContainerd1CheckpointImageName)
	if err != nil {
		if err != ErrMediaTypeNotFound {
			return nil, err
		}
	}
	imageName := ""
	if m != nil {
		data, err := content.ReadBlob(ctx, store, *m)
		if err != nil {
			return nil, err
		}
		imageName = string(data)
	}
	i, err := client.GetImage(ctx, imageName)
	if err != nil {
		return nil, err
	}

	return []NewContainerOpts{
		WithImage(i),
	}, nil
}

// WithRestoreRuntime restores the runtime for the container
func WithRestoreRuntime(ctx context.Context, id string, client *Client, checkpoint Image, index *imagespec.Index) ([]NewContainerOpts, error) {
	store := client.ContentStore()
	n, err := GetIndexByMediaType(index, images.MediaTypeContainerd1CheckpointRuntimeName)
	if err != nil {
		if err != ErrMediaTypeNotFound {
			return nil, err
		}
	}
	runtimeName := ""
	if n != nil {
		data, err := content.ReadBlob(ctx, store, *n)
		if err != nil {
			return nil, err
		}
		runtimeName = string(data)
	}

	// restore options if present
	m, err := GetIndexByMediaType(index, images.MediaTypeContainerd1CheckpointRuntimeOptions)
	if err != nil {
		if err != ErrMediaTypeNotFound {
			return nil, err
		}
	}

	var options *ptypes.Any
	if m != nil {
		data, err := content.ReadBlob(ctx, store, *m)
		if err != nil {
			return nil, errors.Wrap(err, "unable to read checkpoint runtime")
		}
		if err := proto.Unmarshal(data, options); err != nil {
			return nil, err
		}
	}
	return []NewContainerOpts{
		WithRuntime(runtimeName, options),
	}, nil
}

// WithRestoreSpec restores the spec from the checkpoint for the container
func WithRestoreSpec(ctx context.Context, id string, client *Client, checkpoint Image, index *imagespec.Index) ([]NewContainerOpts, error) {
	m, err := GetIndexByMediaType(index, images.MediaTypeContainerd1CheckpointConfig)
	if err != nil {
		return nil, err
	}
	store := client.ContentStore()
	data, err := content.ReadBlob(ctx, store, *m)
	if err != nil {
		return nil, errors.Wrap(err, "unable to read checkpoint config")
	}
	var any ptypes.Any
	if err := proto.Unmarshal(data, &any); err != nil {
		return nil, err
	}

	v, err := typeurl.UnmarshalAny(&any)
	if err != nil {
		return nil, err
	}
	spec := v.(*oci.Spec)
	return []NewContainerOpts{
		WithSpec(spec),
	}, nil
}

// WithRestoreSnapshot restores the snapshot from the checkpoint for the container
func WithRestoreSnapshot(ctx context.Context, id string, client *Client, checkpoint Image, index *imagespec.Index) ([]NewContainerOpts, error) {
	imageName := ""
	store := client.ContentStore()
	m, err := GetIndexByMediaType(index, images.MediaTypeContainerd1CheckpointImageName)
	if err != nil {
		if err != ErrMediaTypeNotFound {
			return nil, err
		}
	}
	if m != nil {
		data, err := content.ReadBlob(ctx, store, *m)
		if err != nil {
			return nil, err
		}
		imageName = string(data)
	}
	i, err := client.GetImage(ctx, imageName)
	if err != nil {
		return nil, err
	}

	diffIDs, err := i.(*image).i.RootFS(ctx, client.ContentStore(), platforms.Default())
	if err != nil {
		return nil, err
	}
	parent := identity.ChainID(diffIDs).String()
	if _, err := client.SnapshotService(DefaultSnapshotter).Prepare(ctx, id, parent); err != nil {
		return nil, err
	}
	return []NewContainerOpts{
		WithImage(i),
		WithSnapshot(id),
	}, nil
}
