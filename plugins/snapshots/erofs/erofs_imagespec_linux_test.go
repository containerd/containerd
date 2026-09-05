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
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

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
// It verifies that images.LayerIDs resolves the correct DiffID for each
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
	diffIDs, err := images.LayerIDs(manifestLayers, nil)
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

// mkfsErofsTarIndexOnly runs `mkfs.erofs --tar=i` over tarBytes, producing an
// EROFS metadata-only image whose file data is *not* embedded but instead
// addressed via EROFS multi-device addressing into an external device blob
// containing the original tar bytes verbatim - the layout the "device" role
// layer described in the EROFS image layer format specification (§2.4) is
// designed around ("matching the way mkfs.erofs --tar=i already produces
// metadata images that reference an original tar as a data device").
func mkfsErofsTarIndexOnly(t *testing.T, tarBytes []byte) []byte {
	t.Helper()
	out := filepath.Join(t.TempDir(), "index.erofs")
	cmd := exec.Command("mkfs.erofs", "--tar=i", "--aufs", "--quiet", "-Enoinline_data", out)
	cmd.Stdin = bytes.NewReader(tarBytes)
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "mkfs.erofs --tar=i: %s", output)
	b, err := os.ReadFile(out)
	require.NoError(t, err)
	return b
}

// buildTestTar returns an uncompressed tar archive containing a single file
// name -> content.
func buildTestTar(t *testing.T, name, content string) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name: name,
		Mode: 0644,
		Size: int64(len(content)),
	}))
	_, err := tw.Write([]byte(content))
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	return buf.Bytes()
}

// TestErofsImageSpecDeviceRole exercises the "device" composition role
// (EROFS image layer format specification, §2.4): a layer holding a raw
// byte stream - here, of media type application/vnd.oci.image.layer.v1.tar
// (no compression suffix, so applied verbatim with no attempt at
// decompression) - consumed by the following EROFS metadata layer via
// EROFS multi-device addressing rather than being mounted on its own.
//
// It verifies that the EROFS snapshotter attaches the device layer's blob
// to the metadata layer's mount via the device= option (see
// snapshotter.composeRoleLowers), and that the file the metadata layer
// addresses into the device blob is visible and correct once mounted.
func TestErofsImageSpecDeviceRole(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")
	tempDir, cs, sn, mgr := setupErofsImageSpecTest(t)
	differ := erofsdiffer.NewErofsDiffer(cs)

	tarBytes := buildTestTar(t, "hello.txt", "hello-device-content")
	metaBytes := mkfsErofsTarIndexOnly(t, tarBytes)

	// Layer 0: the device blob - the raw, uncompressed tar stream the
	// layer 1 metadata addresses into. Any media type is permitted for a
	// device-role layer; a plain (bare) tar type here means the differ
	// applies it verbatim with no decompression, so its DiffID is its own
	// descriptor digest. Unlike a raw EROFS or standalone chunk-index
	// layer, a bare tar layer is not self-digest-eligible in
	// images.LayerIDs, so rootfs.diff_ids must supply it explicitly
	// (as a producer following the specification's compatibility
	// recommendation would).
	descDevice := writeBlob(ctx, t, cs, ocispec.MediaTypeImageLayer, tarBytes, map[string]string{
		"org.erofs.role": "device",
	})

	// Layer 1: the EROFS metadata image referencing the device blob above;
	// role-less (top of the stack).
	descMeta := writeBlob(ctx, t, cs, images.MediaTypeErofs, metaBytes, nil)

	manifestLayers := []ocispec.Descriptor{descDevice, descMeta}
	diffIDs, err := images.LayerIDs(manifestLayers, []digest.Digest{descDevice.Digest, descMeta.Digest})
	require.NoError(t, err)
	require.Equal(t, []digest.Digest{descDevice.Digest, descMeta.Digest}, diffIDs)

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
	}

	// The device layer (index 0) was consumed by the metadata layer
	// (index 1) and must not be independently mountable/visible: only the
	// final, top-level view matters.
	viewMounts, err := sn.View(ctx, "view", parent)
	require.NoError(t, err)

	mountPoint := activateAndMount(t, mgr, tempDir, "mount", viewMounts)
	data, err := os.ReadFile(filepath.Join(mountPoint, "hello.txt"))
	require.NoError(t, err, "hello.txt should be visible via the device= mount option")
	require.Equal(t, "hello-device-content", string(data))
}

// TestErofsImageSpecOverlayDataRole exercises the "overlay-data"
// composition role (EROFS image layer format specification, §2.4): an
// EROFS layer supplied to the overlay mount as a data-only lower (using the
// overlayfs "::" lowerdir separator and metacopy=on) rather than a regular
// lowerdir.
//
// Verifying that an overlayfs metacopy file's "trusted.overlay.redirect"
// xattr actually resolves content from the data-only lower is a kernel
// overlayfs behavior, not a containerd one, and requires writing "trusted."
// xattrs into the source tree before running mkfs.erofs - which some
// sandboxed environments silently disallow (setxattr succeeds but the
// value is not persisted). This test verifies what is within containerd's
// control regardless of that: the EROFS snapshotter's composed mount
// options are accepted by the kernel (i.e. composeRoleLowers's
// lowerdir=...::... plus metacopy=on is well-formed) and a non-metacopy
// file in the metadata layer is visible through the composed mount. When
// the sandbox does support "trusted." xattrs, it additionally verifies
// that the redirected file's content resolves correctly.
func TestErofsImageSpecOverlayDataRole(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")
	tempDir, cs, sn, mgr := setupErofsImageSpecTest(t)
	differ := erofsdiffer.NewErofsDiffer(cs)

	dataSrc := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dataSrc, "payload.bin"), []byte("real-file-content-in-data-layer"), 0644))

	metaSrc := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(metaSrc, "normal.txt"), []byte("normal-metadata-file"), 0644))
	redirectPath := filepath.Join(metaSrc, "redirected.txt")
	require.NoError(t, os.WriteFile(redirectPath, nil, 0644))
	xattrsPersist := true
	if err := unix.Setxattr(redirectPath, "trusted.overlay.metacopy", []byte{}, 0); err != nil {
		xattrsPersist = false
	}
	if err := unix.Setxattr(redirectPath, "trusted.overlay.redirect", []byte("/payload.bin"), 0); err != nil {
		xattrsPersist = false
	}
	if v, err := unix.Getxattr(redirectPath, "trusted.overlay.redirect", nil); err != nil || v <= 0 {
		// Some sandboxed environments accept the setxattr call but do not
		// actually persist "trusted." namespace xattrs.
		xattrsPersist = false
	}

	dataBlobPath := filepath.Join(t.TempDir(), "data.erofs")
	require.NoError(t, erofsutils.ConvertErofs(ctx, dataBlobPath, dataSrc, nil))
	dataBytes, err := os.ReadFile(dataBlobPath)
	require.NoError(t, err)

	metaBlobPath := filepath.Join(t.TempDir(), "meta.erofs")
	require.NoError(t, erofsutils.ConvertErofs(ctx, metaBlobPath, metaSrc, nil))
	metaBytes, err := os.ReadFile(metaBlobPath)
	require.NoError(t, err)

	descData := writeBlob(ctx, t, cs, images.MediaTypeErofs, dataBytes, map[string]string{
		"org.erofs.role": "overlay-data",
	})
	descMeta := writeBlob(ctx, t, cs, images.MediaTypeErofs, metaBytes, nil)

	manifestLayers := []ocispec.Descriptor{descData, descMeta}
	diffIDs, err := images.LayerIDs(manifestLayers, nil)
	require.NoError(t, err)

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
	}

	viewMounts, err := sn.View(ctx, "view", parent)
	require.NoError(t, err)

	mountPoint := activateAndMount(t, mgr, tempDir, "mount", viewMounts)

	data, err := os.ReadFile(filepath.Join(mountPoint, "normal.txt"))
	require.NoError(t, err, "non-redirected metadata file should be visible")
	require.Equal(t, "normal-metadata-file", string(data))

	if !xattrsPersist {
		t.Log("sandbox does not persist trusted.overlay.* xattrs; skipping redirect content verification")
		return
	}
	data, err = os.ReadFile(filepath.Join(mountPoint, "redirected.txt"))
	require.NoError(t, err, "metacopy-redirected file should resolve via the overlay-data lower")
	require.Equal(t, "real-file-content-in-data-layer", string(data))
}
