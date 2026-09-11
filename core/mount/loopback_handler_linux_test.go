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
