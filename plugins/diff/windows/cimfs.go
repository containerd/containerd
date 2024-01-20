//go:build windows
// +build windows

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

package windows

import (
	"context"
	"fmt"

	"github.com/Microsoft/hcsshim/pkg/cimfs"
	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/diff"
	"github.com/containerd/containerd/v2/core/metadata"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/pkg/archive"
	"github.com/containerd/containerd/v2/pkg/errdefs"
	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/platforms"
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func init() {
	registry.Register(&plugin.Registration{
		Type: plugins.DiffPlugin,
		ID:   "cimfs",
		Requires: []plugin.Type{
			plugins.MetadataPlugin,
		},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			md, err := ic.GetSingle(plugins.MetadataPlugin)
			if err != nil {
				return nil, err
			}

			if !cimfs.IsCimFSSupported() {
				return nil, fmt.Errorf("host windows version doesn't support CimFS: %w", plugin.ErrSkipPlugin)
			}
			ic.Meta.Platforms = append(ic.Meta.Platforms, platforms.DefaultSpec())
			return NewCimDiff(md.(*metadata.DB).ContentStore())
		},
	})
}

// cimDiff does filesystem comparison and application
// for CimFS specific layer diffs.
type cimDiff struct {
	store content.Store
}

// NewCimDiff is the Windows cim container layer implementation
// for comparing and applying filesystem layers
func NewCimDiff(store content.Store) (CompareApplier, error) {
	return cimDiff{
		store: store,
	}, nil
}

// Apply applies the content associated with the provided digests onto the
// provided mounts. Archive content will be extracted and decompressed if
// necessary.
func (c cimDiff) Apply(ctx context.Context, desc ocispec.Descriptor, mounts []mount.Mount, opts ...diff.ApplyOpt) (d ocispec.Descriptor, err error) {
	layer, parentLayerPaths, err := cimMountsToLayerAndParents(mounts)
	if err != nil {
		return emptyDesc, err
	}
	return applyDiffCommon(ctx, c.store, desc, layer, parentLayerPaths, archive.AsCimContainerLayer(), opts...)
}

// Compare creates a diff between the given mounts and uploads the result
// to the content store.
func (c cimDiff) Compare(ctx context.Context, lower, upper []mount.Mount, opts ...diff.Opt) (d ocispec.Descriptor, err error) {
	// support for generating layer diff of cimfs layers will be added later.
	return emptyDesc, errdefs.ErrNotImplemented
}

func cimMountsToLayerAndParents(mounts []mount.Mount) (string, []string, error) {
	if len(mounts) != 1 {
		return "", nil, fmt.Errorf("%w: number of mounts should always be 1 for Windows layers", errdefs.ErrInvalidArgument)
	}
	mnt := mounts[0]
	if mnt.Type != "CimFS" {
		// This is a special case error. When this is received the diff service
		// will attempt the next differ in the chain.
		return "", nil, errdefs.ErrNotImplemented
	}

	parentLayerPaths, err := mnt.GetParentPaths()
	if err != nil {
		return "", nil, err
	}

	return mnt.Source, parentLayerPaths, nil
}
