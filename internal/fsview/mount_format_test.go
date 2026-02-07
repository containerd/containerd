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

package fsview

import (
	"io/fs"
	"path/filepath"
	"testing"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/errdefs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFormatMountTemplates tests template formatting in mount options.
func TestFormatMountTemplates(t *testing.T) {
	basePath, err := filepath.Abs("testdata/base.erofs")
	require.NoError(t, err)
	upperPath, err := filepath.Abs("testdata/upper1.erofs")
	require.NoError(t, err)

	mounts := []mount.Mount{
		{
			Source:  basePath,
			Type:    "erofs",
			Options: []string{"ro", "loop"},
		},
		{
			Source:  upperPath,
			Type:    "erofs",
			Options: []string{"ro", "loop"},
		},
		{
			Type:   "format/mkdir/overlay",
			Source: "overlay",
			Options: []string{
				// overlay 1 0 reverses the order, putting upper (1) before base (0)
				// In overlay lowerdir, leftmost has highest priority
				"lowerdir={{ overlay 1 0 }}",
			},
		},
	}

	viewFS, err := FSMounts(mounts)
	require.NoError(t, err)
	defer viewFS.Close()

	// Verify we can read files from both layers
	t.Run("file from base layer", func(t *testing.T) {
		data, err := fs.ReadFile(viewFS, "dir2/file2.txt")
		require.NoError(t, err)
		assert.Equal(t, "file2 content\n", string(data))
	})

	t.Run("file from upper layer", func(t *testing.T) {
		data, err := fs.ReadFile(viewFS, "dir1/newfile.txt")
		require.NoError(t, err)
		assert.Equal(t, "new file content\n", string(data))
	})

	t.Run("whited out file should not exist", func(t *testing.T) {
		_, err := viewFS.Open("dir1/file1.txt")
		require.Error(t, err)
		assert.ErrorIs(t, err, fs.ErrNotExist)
	})
}

// TestFormatMountReversedRange tests {{ overlay }} with reversed range.
func TestFormatMountReversedRange(t *testing.T) {
	basePath, err := filepath.Abs("testdata/base.erofs")
	require.NoError(t, err)
	upperPath, err := filepath.Abs("testdata/upper1.erofs")
	require.NoError(t, err)

	mounts := []mount.Mount{
		{
			Source:  basePath,
			Type:    "erofs",
			Options: []string{"ro", "loop"},
		},
		{
			Source:  upperPath,
			Type:    "erofs",
			Options: []string{"ro", "loop"},
		},
		{
			Type:   "format/overlay",
			Source: "overlay",
			Options: []string{
				"lowerdir={{ overlay 1 0 }}",
			},
		},
	}

	viewFS, err := FSMounts(mounts)
	require.NoError(t, err)
	defer viewFS.Close()

	// With reversed range, upper layer (1) comes before base layer (0)
	// So the overlay should prioritize upper layer
	t.Run("file from upper layer takes precedence", func(t *testing.T) {
		data, err := fs.ReadFile(viewFS, "dir1/newfile.txt")
		require.NoError(t, err)
		assert.Equal(t, "new file content\n", string(data))
	})
}

// TestFormatMountIndexes tests {{ mount }} template with single mount.
func TestFormatMountIndexes(t *testing.T) {
	basePath, err := filepath.Abs("testdata/base.erofs")
	require.NoError(t, err)

	mounts := []mount.Mount{
		{
			Source:  basePath,
			Target:  "/mnt/base",
			Type:    "erofs",
			Options: []string{"ro", "loop"},
		},
		{
			Type:   "format/overlay",
			Source: "overlay",
			Options: []string{
				"lowerdir={{ mount 0 }}",
			},
		},
	}

	viewFS, err := FSMounts(mounts)
	require.NoError(t, err)
	defer viewFS.Close()

	// Verify we can read from the mount
	data, err := fs.ReadFile(viewFS, "dir1/file1.txt")
	require.NoError(t, err)
	assert.Equal(t, "file1 content\n", string(data))
}

// TestFormatMountUnsupportedType tests that unsupported mount types return an error.
func TestFormatMountUnsupportedType(t *testing.T) {
	mounts := []mount.Mount{
		{
			Source: "/dev/sda1",
			Type:   "ext4", // Not supported in fsview
		},
		{
			Type:   "format/overlay",
			Source: "overlay",
			Options: []string{
				"lowerdir={{ mount 0 }}",
			},
		},
	}

	_, err := FSMounts(mounts)
	require.Error(t, err)
	assert.ErrorIs(t, err, errdefs.ErrNotImplemented)
}
