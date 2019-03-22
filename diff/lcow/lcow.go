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

package lcow

import (
	"context"
	"io"
	"os"
	"path"
	"time"

	"github.com/Microsoft/go-winio/pkg/security"
	"github.com/Microsoft/hcsshim/ext4/tar2ext4"
	"github.com/containerd/containerd/archive/compression"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/diff"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/metadata"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/plugin"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

const (
	// maxLcowVhdSizeGB is the max size in GB of any layer
	maxLcowVhdSizeGB = 128 * 1024 * 1024 * 1024
)

func init() {
	plugin.Register(&plugin.Registration{
		Type: plugin.DiffPlugin,
		ID:   "windows-lcow",
		Requires: []plugin.Type{
			plugin.MetadataPlugin,
		},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			md, err := ic.Get(plugin.MetadataPlugin)
			if err != nil {
				return nil, err
			}

			ic.Meta.Platforms = append(ic.Meta.Platforms, ocispec.Platform{
				OS:           "linux",
				Architecture: "amd64",
			})
			return NewWindowsLcowDiff(md.(*metadata.DB).ContentStore())
		},
	})
}

// CompareApplier handles both comparison and
// application of layer diffs.
type CompareApplier interface {
	diff.Applier
	diff.Comparer
}

// windowsLcowDiff does filesystem comparison and application
// for Windows specific Linux layer diffs.
type windowsLcowDiff struct {
	store content.Store
}

var emptyDesc = ocispec.Descriptor{}

// NewWindowsLcowDiff is the Windows LCOW container layer implementation
// for comparing and applying Linux filesystem layers on Windows
func NewWindowsLcowDiff(store content.Store) (CompareApplier, error) {
	return windowsLcowDiff{
		store: store,
	}, nil
}

// Apply applies the content associated with the provided digests onto the
// provided mounts. Archive content will be extracted and decompressed if
// necessary.
func (s windowsLcowDiff) Apply(ctx context.Context, desc ocispec.Descriptor, mounts []mount.Mount) (d ocispec.Descriptor, err error) {
	t1 := time.Now()
	defer func() {
		if err == nil {
			log.G(ctx).WithFields(logrus.Fields{
				"d":     time.Since(t1),
				"dgst":  desc.Digest,
				"size":  desc.Size,
				"media": desc.MediaType,
			}).Debugf("diff applied")
		}
	}()

	layer, _, err := mountsToLayerAndParents(mounts)
	if err != nil {
		return emptyDesc, err
	}

	isCompressed, err := images.IsCompressedDiff(ctx, desc.MediaType)
	if err != nil {
		return emptyDesc, errors.Wrapf(errdefs.ErrNotImplemented, "unsupported diff media type: %v", desc.MediaType)
	}

	ra, err := s.store.ReaderAt(ctx, desc)
	if err != nil {
		return emptyDesc, errors.Wrap(err, "failed to get reader from content store")
	}
	defer ra.Close()
	rdr := content.NewReader(ra)
	if isCompressed {
		ds, err := compression.DecompressStream(rdr)
		if err != nil {
			return emptyDesc, err
		}
		defer ds.Close()
		rdr = ds
	}
	// Calculate the Digest as we go
	digester := digest.Canonical.Digester()
	rc := &readCounter{
		r: io.TeeReader(rdr, digester.Hash()),
	}

	layerPath := path.Join(layer, "layer.vhd")
	outFile, err := os.Create(layerPath)
	if err != nil {
		return emptyDesc, err
	}
	defer func() {
		if err != nil {
			outFile.Close()
			os.Remove(layerPath)
		}
	}()

	err = tar2ext4.Convert(rc, outFile, tar2ext4.ConvertWhiteout, tar2ext4.AppendVhdFooter, tar2ext4.MaximumDiskSize(maxLcowVhdSizeGB))
	if err != nil {
		return emptyDesc, errors.Wrapf(err, "failed to convert tar to ext4 vhd")
	}
	outFile.Close()

	err = security.GrantVmGroupAccess(layerPath)
	if err != nil {
		return emptyDesc, errors.Wrapf(err, "failed GrantVmGroupAccess on layer vhd: %v", layerPath)
	}

	return ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayer,
		Size:      rc.c,
		Digest:    digester.Digest(),
	}, nil
}

// Compare creates a diff between the given mounts and uploads the result
// to the content store.
func (s windowsLcowDiff) Compare(ctx context.Context, lower, upper []mount.Mount, opts ...diff.Opt) (d ocispec.Descriptor, err error) {
	return emptyDesc, errdefs.ErrNotImplemented
}

type readCounter struct {
	r io.Reader
	c int64
}

func (rc *readCounter) Read(p []byte) (n int, err error) {
	n, err = rc.r.Read(p)
	rc.c += int64(n)
	return
}

func mountsToLayerAndParents(mounts []mount.Mount) (string, []string, error) {
	if len(mounts) != 1 {
		return "", nil, errors.Wrap(errdefs.ErrInvalidArgument, "number of mounts should always be 1 for Windows lcow-layers")
	}
	mnt := mounts[0]
	if mnt.Type != "lcow-layer" {
		return "", nil, errors.Wrap(errdefs.ErrInvalidArgument, "mount layer type must be lcow-layer")
	}

	parentLayerPaths, err := mnt.GetParentPaths()
	if err != nil {
		return "", nil, err
	}

	return mnt.Source, parentLayerPaths, nil
}
