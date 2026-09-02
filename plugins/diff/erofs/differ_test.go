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

package erofs

import (
	"context"
	"testing"

	"github.com/containerd/errdefs"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/containerd/containerd/v2/core/mount"
)

// TestApplyReadOnlyMounts covers the differ's refusal to apply into a snapshot
// the snapshotter has already populated (e.g. served from a layer content
// cache), which it signals by handing out read-only mounts. Such content is
// shared with every other snapshot of that layer, so writing into it would
// corrupt content we do not own.
//
// The error must be ErrFailedPrecondition and never ErrNotImplemented: the diff
// service only falls through to the next differ on ErrNotImplemented (see
// plugins/services/diff/local.go), and no other differ may write this layer
// either.
func TestApplyReadOnlyMounts(t *testing.T) {
	ctx := context.Background()
	// A store is never reached: the refusal happens before any content is read.
	d := erofsDiff{}

	// An erofs layer blob, which is read-only whatever its options say.
	erofsMount := mount.Mount{
		Type:    "erofs",
		Source:  "/var/lib/containerd/snapshots/1/layer.erofs",
		Options: []string{"ro", "loop"},
	}
	// A populated snapshot stacked over its parents: no upperdir, so the
	// overlay is read-only, which is what marks it populated.
	overlayMounts := []mount.Mount{
		erofsMount,
		{Type: "erofs", Source: "/var/lib/containerd/snapshots/2/layer.erofs", Options: []string{"ro", "loop"}},
		{
			Type:    "format/mkdir/overlay",
			Source:  "overlay",
			Options: []string{"lowerdir={{ overlay 0 1 }}", "ro"},
		},
	}

	for _, tc := range []struct {
		name   string
		mounts []mount.Mount
	}{
		{name: "erofs layer", mounts: []mount.Mount{erofsMount}},
		{name: "read-only overlay", mounts: overlayMounts},
	} {
		t.Run(tc.name, func(t *testing.T) {
			desc := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageLayerGzip}
			_, err := d.Apply(ctx, desc, tc.mounts)
			require.Error(t, err)
			assert.ErrorIs(t, err, errdefs.ErrFailedPrecondition)
			assert.NotErrorIs(t, err, errdefs.ErrNotImplemented,
				"must not fall through to the next differ")
		})
	}

	// A writable snapshot is applied normally: the refusal must not fire, so
	// this gets as far as rejecting the media type instead.
	t.Run("writable mounts are not refused", func(t *testing.T) {
		mounts := []mount.Mount{{
			Type:    "bind",
			Source:  "/var/lib/containerd/snapshots/3/fs",
			Options: []string{"rw", "rbind"},
		}}
		desc := ocispec.Descriptor{MediaType: "application/vnd.oci.image.layer.v1.tar+unsupported"}
		_, err := d.Apply(ctx, desc, mounts)
		require.Error(t, err)
		assert.NotErrorIs(t, err, errdefs.ErrFailedPrecondition)
	})
}
