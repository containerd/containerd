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
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/containerd/log/logtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/containerd/containerd/v2/internal/dmverity"
)

// TestGetDmverityOptions tests the block size configuration
func TestGetDmverityOptions(t *testing.T) {
	// tar-index mode uses 512-byte blocks
	opts := (&erofsDiff{enableTarIndex: true, enableDmverity: true}).getDmverityOptions()
	assert.Equal(t, uint32(512), opts.DataBlockSize)
	assert.Equal(t, uint32(512), opts.HashBlockSize)

	// regular mode uses 4096-byte blocks
	opts = (&erofsDiff{enableTarIndex: false, enableDmverity: true}).getDmverityOptions()
	assert.Equal(t, uint32(4096), opts.DataBlockSize)
	assert.Equal(t, uint32(4096), opts.HashBlockSize)
}

// TestFormatDmverityLayer tests the layer formatting logic
func TestFormatDmverityLayer(t *testing.T) {
	supported, err := dmverity.IsSupported()
	if err != nil || !supported {
		t.Skip("dm-verity is not supported on this system")
	}

	ctx := logtest.WithT(context.Background(), t)
	tmpDir := t.TempDir()

	t.Run("formats layer and creates metadata", func(t *testing.T) {
		d := &erofsDiff{enableDmverity: true, enableTarIndex: false}
		layerPath := filepath.Join(tmpDir, "layer.erofs")
		require.NoError(t, os.WriteFile(layerPath, make([]byte, 8192), 0644))

		require.NoError(t, d.formatDmverityLayer(ctx, layerPath))

		metadata, err := dmverity.ReadMetadata(layerPath)
		require.NoError(t, err)
		assert.NotEmpty(t, metadata.RootHash)
		assert.Greater(t, metadata.HashOffset, uint64(0))
		assert.Equal(t, uint64(8192), metadata.HashOffset)
	})

	t.Run("skips formatting if metadata already exists", func(t *testing.T) {
		d := &erofsDiff{enableDmverity: true, enableTarIndex: false}
		layerPath := filepath.Join(tmpDir, "layer-idempotent.erofs")
		require.NoError(t, os.WriteFile(layerPath, make([]byte, 8192), 0644))

		// First format
		require.NoError(t, d.formatDmverityLayer(ctx, layerPath))
		metadata1, _ := dmverity.ReadMetadata(layerPath)
		origHash := metadata1.RootHash

		require.NoError(t, d.formatDmverityLayer(ctx, layerPath))
		metadata2, _ := dmverity.ReadMetadata(layerPath)
		assert.Equal(t, origHash, metadata2.RootHash)
	})

	t.Run("uses 4096-byte blocks in regular mode", func(t *testing.T) {
		d := &erofsDiff{enableDmverity: true, enableTarIndex: false}
		layerPath := filepath.Join(tmpDir, "layer-4k.erofs")
		require.NoError(t, os.WriteFile(layerPath, make([]byte, 8192), 0644))

		require.NoError(t, d.formatDmverityLayer(ctx, layerPath))

		metadata, _ := dmverity.ReadMetadata(layerPath)
		assert.Equal(t, uint64(8192), metadata.HashOffset)
	})

	t.Run("uses 512-byte blocks in tar-index mode", func(t *testing.T) {
		d := &erofsDiff{enableDmverity: true, enableTarIndex: true}
		layerPath := filepath.Join(tmpDir, "layer-512.erofs")
		require.NoError(t, os.WriteFile(layerPath, make([]byte, 1024), 0644))

		require.NoError(t, d.formatDmverityLayer(ctx, layerPath))

		metadata, _ := dmverity.ReadMetadata(layerPath)
		assert.Equal(t, uint64(1024), metadata.HashOffset)
	})

	t.Run("returns error for non-existent file", func(t *testing.T) {
		d := &erofsDiff{enableDmverity: true, enableTarIndex: false}
		err := d.formatDmverityLayer(ctx, filepath.Join(tmpDir, "missing.erofs"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to stat layer blob")
	})

	t.Run("rounds up non-aligned file size to block boundary", func(t *testing.T) {
		d := &erofsDiff{enableDmverity: true, enableTarIndex: false}
		layerPath := filepath.Join(tmpDir, "layer-unaligned.erofs")
		// Write 5000 bytes (not aligned to 4096), should round up to 8192
		require.NoError(t, os.WriteFile(layerPath, make([]byte, 5000), 0644))

		require.NoError(t, d.formatDmverityLayer(ctx, layerPath))

		metadata, err := dmverity.ReadMetadata(layerPath)
		require.NoError(t, err)
		// Hash offset should be rounded up to next 4096-byte boundary
		assert.Equal(t, uint64(8192), metadata.HashOffset)
	})
}
