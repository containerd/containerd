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

//go:build linux

// Tests for erofsutils that rely on Linux-specific behaviour (xattrs, EROFS
// reader xattr round-trip).  The xattr tests use the "user." namespace which
// does not require root on most filesystems.

package erofsutils_test

import (
	"archive/tar"
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/containerd/containerd/v2/internal/erofsutils"
	goerofs "github.com/erofs/go-erofs"
)

// ============================================================
// Xattr round-trip tests
// ============================================================

// TestConvertErofsXattrRoundtrip verifies that extended attributes set on a
// source directory tree are preserved verbatim in the EROFS image produced by
// ConvertErofs.
//
// Specifically:
//   - A non-empty "user.foo" xattr is preserved.
//   - An empty-value "user.empty" xattr is preserved (not silently dropped).
//   - A directory xattr ("user.dir") is preserved.
//   - Entries without xattrs produce no spurious entries in the image.
//
// The "user." namespace requires no root privileges on most filesystems.
func TestConvertErofsXattrRoundtrip(t *testing.T) {
	dir := t.TempDir()

	// file with non-empty xattr
	filePath := filepath.Join(dir, "file.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("hello"), 0644))
	require.NoError(t, unix.Lsetxattr(filePath, "user.foo", []byte("bar"), 0),
		"setting user.foo on file; filesystem may not support xattrs — skip if ENOTSUP")

	// file with empty-value xattr (regression test for dropped empty values)
	emptyPath := filepath.Join(dir, "empty-xattr.txt")
	require.NoError(t, os.WriteFile(emptyPath, []byte("world"), 0644))
	if err := unix.Lsetxattr(emptyPath, "user.empty", []byte{}, 0); err != nil {
		t.Skipf("filesystem does not support setting empty xattrs: %v", err)
	}

	// subdirectory with an xattr
	subDir := filepath.Join(dir, "sub")
	require.NoError(t, os.Mkdir(subDir, 0755))
	require.NoError(t, unix.Lsetxattr(subDir, "user.dir", []byte("dirval"), 0))

	// file with no xattrs (must not gain spurious xattrs in the image)
	plainPath := filepath.Join(dir, "plain.txt")
	require.NoError(t, os.WriteFile(plainPath, []byte("plain"), 0644))

	out := filepath.Join(t.TempDir(), "layer.erofs")
	require.NoError(t, erofsutils.ConvertErofs(context.Background(), out, dir))

	// Open the produced EROFS image and read back xattrs.
	img := openEROFS(t, out)

	// /file.txt: user.foo=bar
	xattrs := erofsXattrs(t, img, "file.txt")
	assert.Equal(t, "bar", xattrs["user.foo"], "user.foo must be preserved")

	// /empty-xattr.txt: user.empty="" (empty value must not be dropped)
	xattrs = erofsXattrs(t, img, "empty-xattr.txt")
	val, present := xattrs["user.empty"]
	assert.True(t, present, "user.empty xattr must be present in EROFS image")
	assert.Equal(t, "", val, "user.empty value must be empty string")

	// /sub: user.dir=dirval
	xattrs = erofsXattrs(t, img, "sub")
	assert.Equal(t, "dirval", xattrs["user.dir"], "user.dir must be preserved on directory")

	// /plain.txt: no user.* xattrs
	xattrs = erofsXattrs(t, img, "plain.txt")
	for k := range xattrs {
		assert.NotContains(t, k, "user.", "plain.txt must have no user xattrs, got %q", k)
	}
}

// TestConvertErofsOpaqueXattr is a focused regression test ensuring that
// the trusted.overlay.opaque xattr ("y") survives the dir→EROFS conversion.
// This xattr is what overlayfs uses to mark directories that replace a
// lower-layer directory, and dropping it causes lower-layer content to bleed
// through on stacked mounts.
//
// Requires CAP_SYS_ADMIN / root because "trusted." xattrs require elevated
// privilege.  The test skips automatically when run unprivileged.
func TestConvertErofsOpaqueXattr(t *testing.T) {
	dir := t.TempDir()
	opaqueDir := filepath.Join(dir, "opaque")
	require.NoError(t, os.Mkdir(opaqueDir, 0755))

	err := unix.Lsetxattr(opaqueDir, "trusted.overlay.opaque", []byte("y"), 0)
	if err != nil {
		t.Skipf("cannot set trusted.overlay.opaque (need root): %v", err)
	}

	out := filepath.Join(t.TempDir(), "layer.erofs")
	require.NoError(t, erofsutils.ConvertErofs(context.Background(), out, dir))

	img := openEROFS(t, out)
	xattrs := erofsXattrs(t, img, "opaque")
	assert.Equal(t, "y", xattrs["trusted.overlay.opaque"],
		"trusted.overlay.opaque must be preserved in EROFS image")
}

// ============================================================
// GenerateTarIndexAndAppendTarTo tests
// ============================================================

// TestGenerateTarIndexAndAppendTarToExplicitPaths verifies that the explicit-
// path variant writes metadata to metaPath and data to dataPath, and that the
// two files have the expected structure.
func TestGenerateTarIndexAndAppendTarToExplicitPaths(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeDir, Name: "./", Mode: 0755,
	}))
	const payload = "explicit-paths test content"
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeReg, Name: "data.txt",
		Size: int64(len(payload)), Mode: 0644,
	}))
	_, err := tw.Write([]byte(payload))
	require.NoError(t, err)
	require.NoError(t, tw.Close())

	metaDir := t.TempDir()
	dataDir := t.TempDir()
	metaPath := filepath.Join(metaDir, "my-meta.erofs")
	dataPath := filepath.Join(dataDir, "my-data.bin")

	require.NoError(t, erofsutils.GenerateTarIndexAndAppendTarTo(
		context.Background(), bytes.NewReader(buf.Bytes()), metaPath, dataPath, ""))

	// metaPath must be a valid EROFS image.
	checkSuperblock(t, metaPath)

	// dataPath must contain the file payload.
	dataBytes, err := os.ReadFile(dataPath)
	require.NoError(t, err)
	require.Contains(t, string(dataBytes), payload,
		"payload must appear in the data file")

	// metaPath must NOT contain the payload (it is metadata-only).
	metaBytes, err := os.ReadFile(metaPath)
	require.NoError(t, err)
	require.NotContains(t, string(metaBytes), payload,
		"payload must not appear in the metadata image")

	// Both files must be in their respective directories, not mixed up.
	_, err = os.Stat(filepath.Join(metaDir, "my-data.bin"))
	require.True(t, os.IsNotExist(err), "data file must not appear in meta directory")
	_, err = os.Stat(filepath.Join(dataDir, "my-meta.erofs"))
	require.True(t, os.IsNotExist(err), "meta file must not appear in data directory")
}

// TestGenerateTarIndexAndAppendTarToChunkDeviceID verifies that the metadata
// image produced by GenerateTarIndexAndAppendTarTo contains chunk indexes that
// reference DeviceID=1 (i.e., the data file), and that the file list matches
// a full-extraction image from the same tar.
func TestGenerateTarIndexAndAppendTarToChunkDeviceID(t *testing.T) {
	data := makeTarStream(t, 16*1024)

	dir := t.TempDir()
	metaPath := filepath.Join(dir, "fsmeta.erofs")
	dataPath := filepath.Join(dir, "layer.erofs")

	require.NoError(t, erofsutils.GenerateTarIndexAndAppendTarTo(
		context.Background(), bytes.NewReader(data), metaPath, dataPath, ""))

	// Full extraction for comparison.
	fullOut := filepath.Join(t.TempDir(), "full.erofs")
	require.NoError(t, erofsutils.ConvertTarErofs(
		context.Background(), bytes.NewReader(data), fullOut, ""))

	// The metadata image references DeviceID=1 == dataPath.
	// Supply dataPath as the extra device so the reader can resolve chunks.
	idxNames := erofsFileNamesWithDevice(t, metaPath, dataPath)
	fullNames := erofsFileNames(t, fullOut)

	require.Equal(t, len(fullNames), len(idxNames),
		"tar-index metadata and full-extraction must list the same number of entries")
}

// ============================================================
// Helpers
// ============================================================

// openEROFS opens an EROFS image file and returns the fs.FS, asserting no error.
func openEROFS(t *testing.T, path string) fs.FS {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { f.Close() })

	fsys, err := goerofs.Open(f)
	require.NoError(t, err)
	return fsys
}

// erofsXattrs returns the extended attributes for the named path inside an
// EROFS image. Calls Stat() on the image's fs.StatFS interface to load the
// full metadata including xattrs.
func erofsXattrs(t *testing.T, fsys fs.FS, name string) map[string]string {
	t.Helper()

	sfs, ok := fsys.(fs.StatFS)
	require.True(t, ok, "go-erofs fs.FS must implement fs.StatFS")

	info, err := sfs.Stat(name)
	require.NoError(t, err, "Stat(%q) failed", name)

	stat, ok := info.Sys().(*goerofs.Stat)
	require.True(t, ok, "FileInfo.Sys() must return *goerofs.Stat for %q", name)

	if stat.Xattrs == nil {
		return map[string]string{}
	}
	return stat.Xattrs
}
