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
	"os"
	"testing"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/erofs/go-erofs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEROFSBaseLayer tests reading files from a base EROFS layer.
func TestEROFSBaseLayer(t *testing.T) {
	basePath := "testdata/base.erofs"

	f, err := os.Open(basePath)
	require.NoError(t, err)
	defer f.Close()

	efs, err := erofs.EroFS(f)
	require.NoError(t, err)

	testCases := []struct {
		name     string
		path     string
		wantErr  bool
		wantData string
	}{
		{
			name:     "file1 exists in dir1",
			path:     "dir1/file1.txt",
			wantData: "file1 content\n",
		},
		{
			name:     "file2 exists in dir2",
			path:     "dir2/file2.txt",
			wantData: "file2 content\n",
		},
		{
			name:     "subfile exists in dir2/subdir",
			path:     "dir2/subdir/subfile.txt",
			wantData: "subfile content\n",
		},
		{
			name:    "nonexistent file",
			path:    "nonexistent.txt",
			wantErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := fs.ReadFile(efs, tc.path)
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.wantData, string(data))
			}
		})
	}
}

// TestEROFSOverlayWithWhiteout tests overlay with EROFS layers including whiteout handling.
func TestEROFSOverlayWithWhiteout(t *testing.T) {
	basePath := "testdata/base.erofs"
	upperPath := "testdata/upper1.erofs"

	mounts := []mount.Mount{
		{
			Type:   "erofs",
			Source: basePath,
		},
		{
			Type:   "erofs",
			Source: upperPath,
		},
	}

	viewFS, err := FSMounts(mounts)
	require.NoError(t, err)
	defer viewFS.Close()

	layers := []fs.FS{viewFS}
	overlayFS, err := NewOverlayFS(layers)
	require.NoError(t, err)

	testCases := []struct {
		name    string
		path    string
		wantErr bool
		desc    string
	}{
		{
			name:    "whited out file should not exist",
			path:    "dir1/file1.txt",
			wantErr: true,
			desc:    "file1.txt has .wh.file1.txt in upper layer",
		},
		{
			name:    "new file from upper layer exists",
			path:    "dir1/newfile.txt",
			wantErr: false,
			desc:    "newfile.txt was added in upper layer",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := overlayFS.Open(tc.path)
			if tc.wantErr {
				require.Error(t, err, tc.desc)
			} else {
				require.NoError(t, err, tc.desc)
			}
		})
	}
}

// TestEROFSWithFSMounts tests using FSMounts to handle EROFS overlay.
func TestEROFSWithFSMounts(t *testing.T) {
	basePath := "testdata/base.erofs"

	mounts := []mount.Mount{
		{
			Type:   "erofs",
			Source: basePath,
		},
	}

	viewFS, err := FSMounts(mounts)
	require.NoError(t, err)
	defer viewFS.Close()

	// Test that we can read files through FSMounts
	testCases := []struct {
		name string
		path string
	}{
		{
			name: "read file1",
			path: "dir1/file1.txt",
		},
		{
			name: "read file2",
			path: "dir2/file2.txt",
		},
		{
			name: "read subfile",
			path: "dir2/subdir/subfile.txt",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := fs.ReadFile(viewFS, tc.path)
			require.NoError(t, err)
		})
	}
}

// TestEROFSMultipleLayersOverlay tests overlay with multiple EROFS layers.
func TestEROFSMultipleLayersOverlay(t *testing.T) {
	basePath := "testdata/base.erofs"
	upperPath := "testdata/upper1.erofs"

	// Open base layer
	baseFile, err := os.Open(basePath)
	require.NoError(t, err)
	defer baseFile.Close()

	baseFS, err := erofs.EroFS(baseFile)
	require.NoError(t, err)

	// Open upper layer
	upperFile, err := os.Open(upperPath)
	require.NoError(t, err)
	defer upperFile.Close()

	upperFS, err := erofs.EroFS(upperFile)
	require.NoError(t, err)

	// Create overlay with upper layer first (highest priority)
	layers := []fs.FS{upperFS, baseFS}
	overlayFS, err := NewOverlayFS(layers)
	require.NoError(t, err)

	t.Run("file from upper layer", func(t *testing.T) {
		data, err := fs.ReadFile(overlayFS, "dir1/newfile.txt")
		require.NoError(t, err)
		assert.Equal(t, "new file content\n", string(data))
	})

	t.Run("file from base layer", func(t *testing.T) {
		data, err := fs.ReadFile(overlayFS, "dir2/file2.txt")
		require.NoError(t, err)
		assert.Equal(t, "file2 content\n", string(data))
	})

	t.Run("file in opaque directory should not exist", func(t *testing.T) {
		_, err := overlayFS.Open("dir1/file1.txt")
		require.Error(t, err)
		assert.ErrorIs(t, err, fs.ErrNotExist)
	})

	t.Run("directory listing respects whiteout", func(t *testing.T) {
		entries, err := fs.ReadDir(overlayFS, "dir1")
		require.NoError(t, err)

		// Should only have newfile.txt, not file1.txt
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}

		assert.Contains(t, names, "newfile.txt")
		assert.NotContains(t, names, "file1.txt")
		assert.NotContains(t, names, "file2.txt")
	})
}

// TestEROFSWhiteoutDetection tests that whiteout files are correctly detected.
func TestEROFSWhiteoutDetection(t *testing.T) {
	upperPath := "testdata/upper2.erofs"

	upperFile, err := os.Open(upperPath)
	require.NoError(t, err)
	defer upperFile.Close()

	upperFS, err := erofs.EroFS(upperFile)
	require.NoError(t, err)

	// Check the whiteout file
	fi, err := fs.Stat(upperFS, "dir1/file1.txt")
	require.NoError(t, err)

	// Verify it's detected as a whiteout
	assert.True(t, isWhiteout(fi), "should detect file1.txt as whiteout")
	assert.Equal(t, fs.ModeCharDevice, fi.Mode()&fs.ModeCharDevice, "should be char device")
}
