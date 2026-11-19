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
	"archive/tar"
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/internal/erofsutils"
	"github.com/containerd/containerd/v2/plugins/content/local"
)

// applyLayer applies a one-file tar layer with a differ built from opts into
// a fresh snapshot directory and returns that directory.
func applyLayer(t *testing.T, opts ...DifferOpt) string {
	t.Helper()
	ctx := context.Background()

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	require.NoError(t, tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: "hello", Size: 5, Mode: 0644}))
	_, err := tw.Write([]byte("hello"))
	require.NoError(t, err)
	require.NoError(t, tw.Close())

	cs, err := local.NewStore(t.TempDir())
	require.NoError(t, err)
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayer,
		Digest:    digest.FromBytes(buf.Bytes()),
		Size:      int64(buf.Len()),
	}
	require.NoError(t, content.WriteBlob(ctx, cs, "layer", bytes.NewReader(buf.Bytes()), desc))

	// A snapshot directory as the EROFS snapshotter prepares it.
	snapshot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(snapshot, ".erofslayer"), nil, 0644))
	require.NoError(t, os.Mkdir(filepath.Join(snapshot, "fs"), 0755))
	mounts := []mount.Mount{{Type: "bind", Source: filepath.Join(snapshot, "fs"), Options: []string{"rw", "rbind"}}}

	_, err = NewErofsDiffer(cs, opts...).Apply(ctx, desc, mounts)
	require.NoError(t, err)
	return snapshot
}

// TestApplyConverterSelection verifies which converter the differ uses: the
// Go converter by default, mkfs.erofs with WithMkfs or non-empty
// WithMkfsOptions. In tar index mode the Go
// converter writes a separate data device, while mkfs.erofs appends the tar to
// the layer blob.
func TestApplyConverterSelection(t *testing.T) {
	_, mkfsErr := exec.LookPath("mkfs.erofs")

	for _, tc := range []struct {
		name       string
		opts       []DifferOpt
		mkfs       bool
		dataDevice bool
	}{
		{name: "go"},
		{name: "go tar index", opts: []DifferOpt{WithTarIndexMode()}, dataDevice: true},
		{name: "mkfs", opts: []DifferOpt{WithMkfs(), WithMkfsOptions([]string{"-T0"})}, mkfs: true},
		{name: "mkfs tar index", opts: []DifferOpt{WithMkfs(), WithTarIndexMode()}, mkfs: true},
		{name: "mkfs options select mkfs", opts: []DifferOpt{WithMkfsOptions([]string{"-T0"})}, mkfs: true},
		{name: "empty mkfs options", opts: []DifferOpt{WithMkfsOptions(nil)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.mkfs && mkfsErr != nil {
				t.Skip("mkfs.erofs not found in PATH")
			}
			snapshot := applyLayer(t, tc.opts...)

			blob, err := os.ReadFile(filepath.Join(snapshot, "layer.erofs"))
			require.NoError(t, err)
			// mkfs.erofs records a volume UUID 48 bytes into the superblock at
			// 1024; the Go converter records none.
			uuid := blob[1024+48 : 1024+64]
			if tc.mkfs {
				assert.NotEqual(t, make([]byte, 16), uuid, "layer must be built by mkfs.erofs")
			} else {
				assert.Equal(t, make([]byte, 16), uuid, "layer must be built by the Go converter")
			}

			devices, err := erofsutils.LayerDevices(snapshot)
			require.NoError(t, err)
			if tc.dataDevice {
				assert.Len(t, devices, 1)
			} else {
				assert.Empty(t, devices)
			}
		})
	}
}

// TestApplyMkfsMissing verifies that a differ configured for mkfs.erofs fails
// when mkfs.erofs is not installed instead of falling back to Go.
func TestApplyMkfsMissing(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	ctx := context.Background()
	cs, err := local.NewStore(t.TempDir())
	require.NoError(t, err)

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	require.NoError(t, tw.Close())
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayer,
		Digest:    digest.FromBytes(buf.Bytes()),
		Size:      int64(buf.Len()),
	}
	require.NoError(t, content.WriteBlob(ctx, cs, "layer", bytes.NewReader(buf.Bytes()), desc))

	snapshot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(snapshot, ".erofslayer"), nil, 0644))
	require.NoError(t, os.Mkdir(filepath.Join(snapshot, "fs"), 0755))
	mounts := []mount.Mount{{Type: "bind", Source: filepath.Join(snapshot, "fs"), Options: []string{"rw", "rbind"}}}

	_, err = NewErofsDiffer(cs, WithMkfs()).Apply(ctx, desc, mounts)
	assert.Error(t, err)
}
