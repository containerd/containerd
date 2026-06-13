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
	"context"
	"fmt"
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

// TestConvertErofsManyFiles verifies that converting a directory does not hold
// a descriptor open per file: a tree with more files than the open file limit
// still converts.
func TestConvertErofsManyFiles(t *testing.T) {
	const files = 512
	dir := t.TempDir()
	for i := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%04d", i)), []byte(fmt.Sprint(i)), 0644))
	}

	var limit unix.Rlimit
	require.NoError(t, unix.Getrlimit(unix.RLIMIT_NOFILE, &limit))
	lowered := limit
	lowered.Cur = 128
	require.NoError(t, unix.Setrlimit(unix.RLIMIT_NOFILE, &lowered))
	defer func() {
		require.NoError(t, unix.Setrlimit(unix.RLIMIT_NOFILE, &limit))
	}()

	out := filepath.Join(t.TempDir(), "layer.erofs")
	require.NoError(t, erofsutils.ConvertErofs(context.Background(), out, dir))

	fsys := openEROFS(t, out)
	data, err := fs.ReadFile(fsys, "f0511")
	require.NoError(t, err)
	assert.Equal(t, "511", string(data))
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
