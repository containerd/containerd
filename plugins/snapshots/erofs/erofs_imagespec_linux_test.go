//go:build linux

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
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/mount"
	mountmanager "github.com/containerd/containerd/v2/core/mount/manager"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/internal/erofsutils"
	"github.com/containerd/containerd/v2/pkg/archive/compression"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/containerd/v2/pkg/testutil"
	"github.com/containerd/containerd/v2/plugins/content/local"
	erofsdiffer "github.com/containerd/containerd/v2/plugins/diff/erofs"
	erofsmount "github.com/containerd/containerd/v2/plugins/mount/erofs"

	bolt "go.etcd.io/bbolt"
)

// writeBlob writes b to the content store under its own digest and returns a
// descriptor for it, merging in any extra annotations.
func writeBlob(ctx context.Context, t *testing.T, cs content.Store, mt string, b []byte, annotations map[string]string) ocispec.Descriptor {
	t.Helper()
	desc := ocispec.Descriptor{
		MediaType:   mt,
		Digest:      digest.FromBytes(b),
		Size:        int64(len(b)),
		Annotations: annotations,
	}
	require.NoError(t, content.WriteBlob(ctx, cs, fmt.Sprintf("test-%s", desc.Digest), bytes.NewReader(b), desc))
	return desc
}

// buildRawErofsLayer creates a single-file EROFS filesystem image containing
// name -> content, and returns its raw (uncompressed) bytes.
func buildRawErofsLayer(ctx context.Context, t *testing.T, name, content string) []byte {
	t.Helper()
	src := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(src, name), []byte(content), 0644))

	blobPath := filepath.Join(t.TempDir(), "blob.erofs")
	require.NoError(t, erofsutils.ConvertErofs(ctx, blobPath, src, nil))

	b, err := os.ReadFile(blobPath)
	require.NoError(t, err)
	return b
}

func zstdCompress(t *testing.T, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := compression.CompressStream(&buf, compression.Zstd)
	require.NoError(t, err)
	_, err = w.Write(b)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	return buf.Bytes()
}

// setupErofsImageSpecTest requires root and a working EROFS kernel module,
// and returns a fresh temp dir, content store, EROFS snapshotter, and mount
// manager (with the EROFS mount handler registered) for driving a manifest
// through Prepare/Apply/Commit/View and mounting the result, as
// core/unpack would. Callers construct their own differ via
// erofsdiffer.NewErofsDiffer(cs), since its unexported return type cannot
// be named in this function's signature.
func setupErofsImageSpecTest(t *testing.T) (tempDir string, cs content.Store, sn snapshots.Snapshotter, mgr mount.Manager) {
	t.Helper()
	testutil.RequiresRoot(t)
	if !FindErofs() {
		t.Skip("check for erofs kernel support failed, skipping test")
	}

	tempDir = t.TempDir()
	var err error
	cs, err = local.NewStore(filepath.Join(tempDir, "content"))
	require.NoError(t, err)

	s, err := NewSnapshotter(filepath.Join(tempDir, "snapshots"))
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	sn = s

	db, err := bolt.Open(filepath.Join(tempDir, "mounts.db"), 0600, nil)
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	mgr, err = mountmanager.NewManager(db, filepath.Join(tempDir, "mount-manager"),
		mountmanager.WithMountHandler("erofs", erofsmount.NewErofsMountHandler()))
	require.NoError(t, err)

	return tempDir, cs, sn, mgr
}

// activateAndMount activates viewMounts and returns the path at which the
// fully composed rootfs is available, handling both the single-mount case
// (Active has the final result directly) and the multi-mount overlay case
// (System must be mounted at a fresh target directory).
func activateAndMount(t *testing.T, mgr mount.Manager, tempDir, name string, viewMounts []mount.Mount) string {
	t.Helper()
	ctx := namespaces.WithNamespace(context.Background(), "test")

	activateInfo, err := mgr.Activate(ctx, name, viewMounts)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, mgr.Deactivate(ctx, name)) })

	if len(activateInfo.System) == 0 {
		require.NotEmpty(t, activateInfo.Active)
		return activateInfo.Active[len(activateInfo.Active)-1].MountPoint
	}
	target := filepath.Join(tempDir, name+"-target")
	require.NoError(t, os.MkdirAll(target, 0755))
	require.NoError(t, mount.All(activateInfo.System, target))
	t.Cleanup(func() { testutil.Unmount(t, target) })
	return target
}

// TestErofsImageSpecMultiLayer exercises pulling and unpacking (at the
// differ/snapshotter layer-composition level) a manifest that uses the
// EROFS image layer format specification
// (https://github.com/erofs/erofs-image-spec):
//
//   - layer 0: a raw application/vnd.erofs "overlay-lower" layer, whose
//     DiffID (per the specification) is its own descriptor digest.
//   - layer 1: a compressed application/vnd.erofs+zstd layer carrying the
//     org.erofs.uncompressed-digest annotation as its sole source of DiffID
//     (rootfs.diff_ids is entirely absent, as the specification allows).
//
// (Standalone application/vnd.erofs.chunk-index.v1 layers, which a consumer
// silently skips during composition, are covered at the core/unpack level -
// see TestUnpackSkipsChunkIndexLayer[Parallel] - since core/unpack never
// creates a snapshot, and so never calls the differ, for such a layer.)
//
// It verifies that images.LayerDiffIDs resolves the correct DiffID for each
// layer, that the EROFS differ's diff.Digest for each layer matches, and
// that the final mounted rootfs contains the content of both layers.
func TestErofsImageSpecMultiLayer(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")
	tempDir, cs, sn, mgr := setupErofsImageSpecTest(t)
	differ := erofsdiffer.NewErofsDiffer(cs)

	// Layer 0: raw EROFS, overlay-lower role.
	rawA := buildRawErofsLayer(ctx, t, "a.txt", "content-a")
	descA := writeBlob(ctx, t, cs, images.MediaTypeErofs, rawA, map[string]string{
		"org.erofs.role": "overlay-lower",
	})

	// Layer 1: zstd-compressed EROFS, no role (top of the overlay-lower
	// stack), DiffID sourced solely from the annotation.
	rawB := buildRawErofsLayer(ctx, t, "b.txt", "content-b")
	uncompressedB := digest.FromBytes(rawB)
	compressedB := zstdCompress(t, rawB)
	descB := writeBlob(ctx, t, cs, images.MediaTypeErofs+"+zstd", compressedB, map[string]string{
		images.AnnotationErofsUncompressedDigest: uncompressedB.String(),
	})

	manifestLayers := []ocispec.Descriptor{descA, descB}

	// rootfs.diff_ids is entirely absent: the only compressed layer
	// (descB) carries the annotation, and the raw layer falls back to its
	// own descriptor digest.
	diffIDs, err := images.LayerDiffIDs(manifestLayers, nil)
	require.NoError(t, err)
	require.Equal(t, []digest.Digest{descA.Digest, uncompressedB}, diffIDs)

	var parent string
	for i, desc := range manifestLayers {
		prepareKey := fmt.Sprintf("prepare-%d", i)
		mounts, err := sn.Prepare(ctx, prepareKey, parent)
		require.NoError(t, err, "prepare layer %d", i)

		diff, err := differ.Apply(ctx, desc, mounts)
		require.NoError(t, err, "apply layer %d", i)
		require.Equal(t, diffIDs[i], diff.Digest, "diff id mismatch for layer %d", i)

		commitKey := fmt.Sprintf("committed-%d", i)
		require.NoError(t, sn.Commit(ctx, commitKey, prepareKey), "commit layer %d", i)
		parent = commitKey

		viewKey := fmt.Sprintf("view-%d", i)
		viewMounts, err := sn.View(ctx, viewKey, commitKey)
		require.NoError(t, err)

		mountPoint := activateAndMount(t, mgr, tempDir, fmt.Sprintf("mount-%d", i), viewMounts)

		if i >= 0 {
			data, err := os.ReadFile(filepath.Join(mountPoint, "a.txt"))
			require.NoError(t, err, "a.txt should be visible at layer %d", i)
			require.Equal(t, "content-a", string(data))
		}
		if i >= 1 {
			data, err := os.ReadFile(filepath.Join(mountPoint, "b.txt"))
			require.NoError(t, err, "b.txt should be visible at layer %d", i)
			require.Equal(t, "content-b", string(data))
		}

		require.NoError(t, sn.Remove(ctx, viewKey))
	}
}
