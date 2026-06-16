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

package server

import (
	"archive/tar"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestCopyNoFollowRegularFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	require.NoError(t, os.WriteFile(src, []byte("hello"), 0o644))

	require.NoError(t, copyNoFollow(src, dst, 0o600))

	data, err := os.ReadFile(dst)
	require.NoError(t, err)
	assert.Equal(t, "hello", string(data))

	info, err := os.Stat(dst)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestCopyNoFollowMissingSource(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "does-not-exist")
	dst := filepath.Join(dir, "dst")

	err := copyNoFollow(src, dst, 0o600)
	require.Error(t, err)
	assert.True(t, errors.Is(err, os.ErrNotExist), "expected ErrNotExist, got %v", err)
	assert.NoFileExists(t, dst)
}

func TestCopyNoFollowSymlinkSourceNotFollowed(t *testing.T) {
	dir := t.TempDir()

	// A stand-in for a file outside the copy that a symlink might point at.
	secret := filepath.Join(dir, "outside-target")
	require.NoError(t, os.WriteFile(secret, []byte("outside-content"), 0o600))

	src := filepath.Join(dir, "container.log")
	require.NoError(t, os.Symlink(secret, src))
	dst := filepath.Join(dir, "dst")

	err := copyNoFollow(src, dst, 0o600)
	require.Error(t, err)
	// A symlink is not a "missing file"; it must surface as an error, not be skipped.
	assert.False(t, errors.Is(err, os.ErrNotExist))
	// And the linked-to content must not have been copied into the destination.
	assert.NoFileExists(t, dst)
}

func TestCopyNoFollowRejectsFIFO(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "fifo")
	require.NoError(t, unix.Mkfifo(src, 0o600))
	dst := filepath.Join(dir, "dst")

	// Must return promptly with an error rather than blocking on the FIFO open.
	err := copyNoFollow(src, dst, 0o600)
	require.Error(t, err)
	assert.False(t, errors.Is(err, os.ErrNotExist))
	assert.NoFileExists(t, dst)
}

func TestCopyNoFollowRejectsDirectory(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "adir")
	require.NoError(t, os.Mkdir(src, 0o700))
	dst := filepath.Join(dir, "dst")

	err := copyNoFollow(src, dst, 0o600)
	require.Error(t, err)
	assert.NoFileExists(t, dst)
}

func TestAssertCheckpointDirSafe(t *testing.T) {
	t.Run("regular files and dirs allowed", func(t *testing.T) {
		root := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(root, "checkpoint"), 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(root, "checkpoint", "img"), []byte("x"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(root, "rootfs-diff.tar"), []byte("x"), 0o600))
		assert.NoError(t, assertCheckpointDirSafe(root))
	})

	t.Run("symlink rejected", func(t *testing.T) {
		root := t.TempDir()
		require.NoError(t, os.Symlink("/some/outside/path", filepath.Join(root, "rootfs-diff.tar")))
		assert.Error(t, assertCheckpointDirSafe(root))
	})

	t.Run("symlink nested in subdir rejected", func(t *testing.T) {
		root := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(root, "checkpoint"), 0o700))
		require.NoError(t, os.Symlink("/some/outside/path", filepath.Join(root, "checkpoint", "pages-1.img")))
		assert.Error(t, assertCheckpointDirSafe(root))
	})

	t.Run("fifo rejected", func(t *testing.T) {
		root := t.TempDir()
		require.NoError(t, unix.Mkfifo(filepath.Join(root, "fifo"), 0o600))
		assert.Error(t, assertCheckpointDirSafe(root))
	})
}

func TestCheckpointArchiveEntryAllowed(t *testing.T) {
	for _, tc := range []struct {
		name    string
		typ     byte
		allowed bool
	}{
		{"regular", tar.TypeReg, true},
		//nolint:staticcheck // TypeRegA is deprecated but external tars may still use it
		{"regular-A", tar.TypeRegA, true},
		{"directory", tar.TypeDir, true},
		{"global-header", tar.TypeXGlobalHeader, true},
		{"symlink", tar.TypeSymlink, false},
		{"hardlink", tar.TypeLink, false},
		{"char-device", tar.TypeChar, false},
		{"block-device", tar.TypeBlock, false},
		{"fifo", tar.TypeFifo, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := checkpointArchiveEntryAllowed(&tar.Header{Typeflag: tc.typ, Name: tc.name})
			assert.Equal(t, tc.allowed, got)
		})
	}
}
