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

package manager

import (
	"context"
	"strings"
	"testing"

	"github.com/containerd/log/logtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/containerd/containerd/v2/core/mount"
)

// TestDmverityTransformer tests the core transformer functionality
func TestDmverityTransformer(t *testing.T) {
	ctx := logtest.WithT(context.Background(), t)
	tr := dmverityTransformer{}

	// Test parsing valid options with roothash, device-name, and hash-offset
	t.Run("parses valid dmverity options", func(t *testing.T) {
		rootHash, deviceName, hashOffset, regularOpts, err := parseDmverityMountOptions([]string{
			"ro",
			"X-containerd.dmverity.roothash=abc123def456789012345678901234567890123456789012345678901234",
			"X-containerd.dmverity.device-name=test-device",
			"X-containerd.dmverity.hash-offset=12288",
		})
		require.NoError(t, err)
		assert.Equal(t, "abc123def456789012345678901234567890123456789012345678901234", rootHash)
		assert.Equal(t, "test-device", deviceName)
		assert.Equal(t, uint64(12288), hashOffset)
		assert.Equal(t, []string{"ro"}, regularOpts)
	})

	// Test unknown dmverity option with = sign
	t.Run("rejects unknown dmverity key-value options", func(t *testing.T) {
		_, _, _, _, err := parseDmverityMountOptions([]string{
			"ro",
			"X-containerd.dmverity.unknown=value",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unknown dmverity option")
	})

	// Test unknown boolean flag (should be ignored for forward compatibility)
	t.Run("ignores unknown dmverity boolean flags", func(t *testing.T) {
		rootHash, deviceName, hashOffset, regularOpts, err := parseDmverityMountOptions([]string{
			"ro",
			"X-containerd.dmverity.roothash=abc123def456789012345678901234567890123456789012345678901234",
			"X-containerd.dmverity.unknown-flag",
		})
		require.NoError(t, err)
		assert.Equal(t, "abc123def456789012345678901234567890123456789012345678901234", rootHash)
		assert.Equal(t, "", deviceName)
		assert.Equal(t, uint64(0), hashOffset)
		assert.Equal(t, []string{"ro"}, regularOpts)
	})

	// Test invalid hash-offset value
	t.Run("rejects invalid hash-offset", func(t *testing.T) {
		_, _, _, _, err := parseDmverityMountOptions([]string{
			"ro",
			"X-containerd.dmverity.hash-offset=invalid",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid hash-offset value")
	})

	// Test regular options are preserved
	t.Run("preserves regular mount options", func(t *testing.T) {
		rootHash, deviceName, hashOffset, regularOpts, err := parseDmverityMountOptions([]string{
			"ro",
			"nodev",
			"X-containerd.dmverity.roothash=abc123",
			"X-containerd.dmverity.device-name=test",
			"X-containerd.dmverity.hash-offset=16384",
			"nosuid",
		})
		require.NoError(t, err)
		assert.Equal(t, "abc123", rootHash)
		assert.Equal(t, "test", deviceName)
		assert.Equal(t, uint64(16384), hashOffset)
		assert.Equal(t, []string{"ro", "nodev", "nosuid"}, regularOpts)
	})

	// Test dmverity options are filtered out from regular options
	t.Run("filters dmverity options from mount options", func(t *testing.T) {
		_, _, _, regularOpts, err := parseDmverityMountOptions([]string{
			"ro",
			"X-containerd.dmverity.roothash=abc123",
			"noatime",
			"X-containerd.dmverity.device-name=test",
			"nodev",
		})
		require.NoError(t, err)
		assert.Equal(t, []string{"ro", "noatime", "nodev"}, regularOpts)
		// Ensure no dmverity options are in regular options
		for _, opt := range regularOpts {
			assert.False(t, strings.HasPrefix(opt, "X-containerd.dmverity."))
		}
	})

	// Test empty options
	t.Run("handles empty options", func(t *testing.T) {
		rootHash, deviceName, hashOffset, regularOpts, err := parseDmverityMountOptions([]string{})
		require.NoError(t, err)
		assert.Equal(t, "", rootHash)
		assert.Equal(t, "", deviceName)
		assert.Equal(t, uint64(0), hashOffset)
		assert.Empty(t, regularOpts)
	})

	// Test Transform will fail without actual device (expected)
	t.Run("Transform fails with non-existent source device", func(t *testing.T) {
		m := mount.Mount{
			Source: "/path/to/nonexistent.erofs",
			Type:   "erofs",
			Options: []string{
				"ro",
				"X-containerd.dmverity.roothash=abc123def456789012345678901234567890123456789012345678901234",
				"X-containerd.dmverity.device-name=test-device",
			},
		}
		_, err := tr.Transform(ctx, m, nil)
		require.Error(t, err)
		// Should fail because device doesn't exist, not because of parsing
		assert.NotContains(t, err.Error(), "unknown dmverity option")
	})
}
