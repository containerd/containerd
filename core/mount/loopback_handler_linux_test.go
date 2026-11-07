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

package mount

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/containerd/continuity/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLoopbackUnmountToleratesDetachedDevice verifies that Unmount
// succeeds when the loop device a symlink points at is already
// detached from its backing file.
func TestLoopbackUnmountToleratesDetachedDevice(t *testing.T) {
	testutil.RequiresRoot(t)

	td := t.TempDir()
	backingFile := createTempFile(t)
	mp := filepath.Join(td, "mp")

	h := LoopbackHandler()
	_, err := h.Mount(context.Background(), Mount{Type: "loop", Source: backingFile}, mp, nil)
	require.NoError(t, err)

	loopdev, err := os.Readlink(mp)
	require.NoError(t, err)
	t.Cleanup(func() {
		// Best effort.
		_ = DetachLoopDevice(loopdev)
	})

	require.NoError(t, DetachLoopDevice(loopdev))

	require.NoError(t, h.Unmount(context.Background(), mp))

	_, err = os.Lstat(mp)
	assert.True(t, os.IsNotExist(err), "the symlink must still be removed even though the device it pointed at was already gone")
}

// TestLoopbackMountedValidatesBackingFile verifies that Mounted
// reports a live loop device as not mounted when it is attached to
// something other than the recorded source, and reports on liveness
// alone for a record which carries no source at all.
func TestLoopbackMountedValidatesBackingFile(t *testing.T) {
	testutil.RequiresRoot(t)

	td := t.TempDir()
	mp := filepath.Join(td, "mp")
	h := LoopbackHandler()
	mc := h.(MountedChecker)

	first := createTempFile(t)
	second := createTempFile(t)

	_, err := h.Mount(context.Background(), Mount{Type: "loop", Source: first}, mp, nil)
	require.NoError(t, err)

	loopdev, err := os.Readlink(mp)
	require.NoError(t, err)
	t.Cleanup(func() {
		// Best effort.
		_ = DetachLoopDevice(loopdev)
	})

	live, err := mc.Mounted(context.Background(), Mount{Type: "loop", Source: first}, mp)
	require.NoError(t, err)
	assert.True(t, live, "the device is attached to the source which was recorded")

	live, err = mc.Mounted(context.Background(), Mount{Type: "loop", Source: second}, mp)
	require.NoError(t, err)
	assert.False(t, live, "the device is live but is not backed by second, so it is not that record's mount")

	live, err = mc.Mounted(context.Background(), Mount{Type: "loop"}, mp)
	require.NoError(t, err)
	assert.True(t, live, "a record with no source falls back to reporting liveness alone")

	require.NoError(t, h.Unmount(context.Background(), mp))
}

// TestLoopbackMountRepairsStaleSymlink verifies that Mount can repair
// a record found not live: a symlink left at mp from an earlier mount
// whose device was detached must not make a fresh Mount at the same
// point fail with EEXIST.
func TestLoopbackMountRepairsStaleSymlink(t *testing.T) {
	testutil.RequiresRoot(t)

	td := t.TempDir()
	mp := filepath.Join(td, "mp")
	h := LoopbackHandler()
	mc := h.(MountedChecker)

	first := createTempFile(t)
	_, err := h.Mount(context.Background(), Mount{Type: "loop", Source: first}, mp, nil)
	require.NoError(t, err)

	loopdev, err := os.Readlink(mp)
	require.NoError(t, err)
	t.Cleanup(func() {
		// Best effort.
		_ = DetachLoopDevice(loopdev)
	})
	require.NoError(t, DetachLoopDevice(loopdev))

	live, err := mc.Mounted(context.Background(), Mount{Type: "loop", Source: first}, mp)
	require.NoError(t, err)
	require.False(t, live, "the stale symlink must be reported as not mounted")

	second := createTempFile(t)
	_, err = h.Mount(context.Background(), Mount{Type: "loop", Source: second}, mp, nil)
	require.NoError(t, err, "repairing a stale symlink must not fail with EEXIST")

	newLoopdev, err := os.Readlink(mp)
	require.NoError(t, err)
	t.Cleanup(func() {
		// Best effort.
		_ = DetachLoopDevice(newLoopdev)
	})

	live, err = mc.Mounted(context.Background(), Mount{Type: "loop", Source: second}, mp)
	require.NoError(t, err)
	assert.True(t, live, "the repaired symlink must be reported as mounted")

	require.NoError(t, h.Unmount(context.Background(), mp))
}
