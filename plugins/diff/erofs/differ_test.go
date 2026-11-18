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
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/containerd/errdefs"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/internal/erofsutils"
	"github.com/containerd/containerd/v2/plugins/content/local"
)

// TestApplyReadOnlyMounts covers the differ's refusal to apply into a snapshot
// the snapshotter has already populated, for example from a layer content
// cache. The snapshotter says so by handing out read-only mounts.
//
// The error must be ErrFailedPrecondition and never ErrNotImplemented:
// plugins/services/diff/local.go treats ErrNotImplemented as the signal to try
// the next differ.
func TestApplyReadOnlyMounts(t *testing.T) {
	ctx := context.Background()
	d := erofsDiff{}

	// All erofs mounts are read-only.
	erofsMount := mount.Mount{
		Type:    "erofs",
		Source:  "/var/lib/containerd/snapshots/1/layer.erofs",
		Options: []string{"ro", "loop"},
	}
	// These mounts are a populated snapshot stacked over its parents. Without
	// an upperdir the overlay is read-only.
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

	// A writable snapshot is applied normally. The read-only guard does not
	// fire. Apply reaches the media type check and fails there.
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

// erofsLayerMounts returns bind mounts pointing at a fresh EROFS-snapshotter
// style layer directory (with the ".erofslayer" marker erofsutils.MountsToLayer
// requires), suitable for passing to erofsDiff.Apply in tests.
func erofsLayerMounts(t *testing.T) []mount.Mount {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".erofslayer"), nil, 0644))
	fs := filepath.Join(dir, "fs")
	require.NoError(t, os.Mkdir(fs, 0755))
	return []mount.Mount{{Type: "bind", Source: fs, Options: []string{"rbind"}}}
}

func newTestContentStore(t *testing.T) content.Store {
	t.Helper()
	cs, err := local.NewStore(t.TempDir())
	require.NoError(t, err)
	return cs
}

func writeTestBlob(t *testing.T, cs content.Store, mt string, b []byte, annotations map[string]string) ocispec.Descriptor {
	t.Helper()
	desc := ocispec.Descriptor{
		MediaType:   mt,
		Digest:      digest.FromBytes(b),
		Size:        int64(len(b)),
		Annotations: annotations,
	}
	require.NoError(t, content.WriteBlob(context.Background(), cs, desc.Digest.String(), bytes.NewReader(b), desc))
	return desc
}

func TestApplyRejectsInvalidRole(t *testing.T) {
	d := erofsDiff{}
	ctx := context.Background()
	mounts := erofsLayerMounts(t)

	desc := ocispec.Descriptor{
		MediaType:   images.MediaTypeErofs,
		Digest:      digest.FromString("test"),
		Annotations: map[string]string{annotationErofsRole: "some-future-role"},
	}
	_, err := d.Apply(ctx, desc, mounts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "some-future-role")
}

func TestApplyAcceptsOverlayLowerRole(t *testing.T) {
	ctx := context.Background()
	mounts := erofsLayerMounts(t)
	cs := newTestContentStore(t)

	// An empty raw EROFS blob is a degenerate but valid input for the
	// fastcopy path; this only exercises that the "overlay-lower" role
	// (and no role at all) are treated as regular mountable EROFS layers.
	desc := writeTestBlob(t, cs, images.MediaTypeErofs, nil, map[string]string{annotationErofsRole: "overlay-lower"})
	d := erofsDiff{store: cs}

	_, err := d.Apply(ctx, desc, mounts)
	require.NoError(t, err)

	_, err = os.Stat(filepath.Join(filepath.Dir(mounts[0].Source), erofsutils.LayerBlobName))
	require.NoError(t, err)
}

// TestApplyChunkIndexNotHandled verifies that the differ refuses a
// standalone application/vnd.erofs.chunk-index.v1 layer with a clear error.
// core/unpack skips chunk-index layers before ever calling into a differ
// (see images.IsSkippableLayerType and core/unpack's parentChainIDsForLayers),
// so in practice this only matters for callers that bypass core/unpack, such
// as pkg/rootfs.ApplyLayerWithOpts (used by client.Image.Unpack).
func TestApplyChunkIndexNotHandled(t *testing.T) {
	d := erofsDiff{}
	ctx := context.Background()
	mounts := erofsLayerMounts(t)

	desc := ocispec.Descriptor{
		MediaType: images.MediaTypeErofsChunkIndex,
		Digest:    digest.FromString("fake-chunk-index-payload"),
		Size:      64,
	}
	_, err := d.Apply(ctx, desc, mounts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), string(images.MediaTypeErofsChunkIndex))
}

func TestApplyUnsupportedErofsSuffix(t *testing.T) {
	d := erofsDiff{}
	ctx := context.Background()
	mounts := erofsLayerMounts(t)

	desc := ocispec.Descriptor{
		MediaType: images.MediaTypeErofs + "+gzip",
		Digest:    digest.FromString("test"),
	}
	_, err := d.Apply(ctx, desc, mounts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported erofs layer suffix")
}

// TestApplyOverlayDataRole verifies that a layer with role "overlay-data" is
// applied like a normal EROFS layer, but written to data.erofs instead of
// layer.erofs (§2.4, §7 step 3 of the EROFS image layer format
// specification), so the EROFS snapshotter can supply it to the overlay
// mount as a data-only lower.
func TestApplyOverlayDataRole(t *testing.T) {
	ctx := context.Background()
	mounts := erofsLayerMounts(t)
	cs := newTestContentStore(t)

	desc := writeTestBlob(t, cs, images.MediaTypeErofs, []byte("fake-erofs-bytes"), map[string]string{annotationErofsRole: erofsRoleOverlayData})
	d := erofsDiff{store: cs}

	diff, err := d.Apply(ctx, desc, mounts)
	require.NoError(t, err)
	// A raw (uncompressed) EROFS blob's DiffID is its own descriptor digest.
	assert.Equal(t, desc.Digest, diff.Digest)

	dataPath := filepath.Join(filepath.Dir(mounts[0].Source), erofsutils.DataBlobName)
	got, err := os.ReadFile(dataPath)
	require.NoError(t, err)
	assert.Equal(t, []byte("fake-erofs-bytes"), got)

	_, err = os.Stat(filepath.Join(filepath.Dir(mounts[0].Source), erofsutils.LayerBlobName))
	require.True(t, os.IsNotExist(err), "overlay-data layer must not also produce layer.erofs")
}

func TestApplyOverlayDataRejectsNonErofsMediaType(t *testing.T) {
	ctx := context.Background()
	mounts := erofsLayerMounts(t)
	cs := newTestContentStore(t)

	desc := writeTestBlob(t, cs, ocispec.MediaTypeImageLayerGzip, []byte("not-erofs"), map[string]string{annotationErofsRole: erofsRoleOverlayData})
	d := erofsDiff{store: cs}

	_, err := d.Apply(ctx, desc, mounts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "overlay-data")
}

// TestApplyDeviceRoleTarGzip verifies that a device-role layer carried as a
// gzip-compressed tar (the same carrier mkfs.erofs --tar=i itself would
// reference as a data device) is decompressed to device.blob, with its
// DiffID computed over the decompressed tar bytes, matching normal OCI
// DiffID semantics for that media type (§5.2, "Device-role layers of other
// media types ... DiffID follows the rules of that media type's
// specification").
func TestApplyDeviceRoleTarGzip(t *testing.T) {
	ctx := context.Background()
	mounts := erofsLayerMounts(t)
	cs := newTestContentStore(t)

	tarBytes := []byte("pretend-this-is-a-tar-stream")
	var gz bytes.Buffer
	gw := gzip.NewWriter(&gz)
	_, err := gw.Write(tarBytes)
	require.NoError(t, err)
	require.NoError(t, gw.Close())

	desc := writeTestBlob(t, cs, ocispec.MediaTypeImageLayerGzip, gz.Bytes(), map[string]string{annotationErofsRole: erofsRoleDevice})
	d := erofsDiff{store: cs}

	diff, err := d.Apply(ctx, desc, mounts)
	require.NoError(t, err)
	assert.Equal(t, digest.FromBytes(tarBytes), diff.Digest)
	assert.Equal(t, int64(len(tarBytes)), diff.Size)

	devicePath := filepath.Join(filepath.Dir(mounts[0].Source), erofsutils.DeviceBlobName)
	got, err := os.ReadFile(devicePath)
	require.NoError(t, err)
	assert.Equal(t, tarBytes, got)
}

// TestApplyDeviceRoleRaw verifies that a device-role layer with no "+suffix"
// (e.g. application/octet-stream, or a custom media type) is copied
// verbatim with no attempt at decompression, and its DiffID is its own
// descriptor digest.
func TestApplyDeviceRoleRaw(t *testing.T) {
	ctx := context.Background()
	mounts := erofsLayerMounts(t)
	cs := newTestContentStore(t)

	raw := []byte("opaque-block-device-content")
	desc := writeTestBlob(t, cs, "application/octet-stream", raw, map[string]string{annotationErofsRole: erofsRoleDevice})
	d := erofsDiff{store: cs}

	diff, err := d.Apply(ctx, desc, mounts)
	require.NoError(t, err)
	assert.Equal(t, desc.Digest, diff.Digest)

	devicePath := filepath.Join(filepath.Dir(mounts[0].Source), erofsutils.DeviceBlobName)
	got, err := os.ReadFile(devicePath)
	require.NoError(t, err)
	assert.Equal(t, raw, got)
}

func TestApplyDeviceRoleUnsupportedSuffix(t *testing.T) {
	ctx := context.Background()
	mounts := erofsLayerMounts(t)
	cs := newTestContentStore(t)

	desc := writeTestBlob(t, cs, "application/octet-stream+bzip2", []byte("x"), map[string]string{annotationErofsRole: erofsRoleDevice})
	d := erofsDiff{store: cs}

	_, err := d.Apply(ctx, desc, mounts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported device layer suffix")
}
