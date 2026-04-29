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
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/pkg/namespaces"
)

// TestCleanupRemovesOrphansAndOrphanAside verifies that Cleanup removes both
// untracked snapshot directories and .orphan-* directories left by
// createSnapshot's rename-aside path on Windows. Since .orphan-* names can
// never be valid tracked IDs, getCleanupDirectories returns them as untracked.
func TestCleanupRemovesOrphansAndOrphanAside(t *testing.T) {
	root := t.TempDir()

	s, err := NewSnapshotter(root)
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })

	ctx := namespaces.WithNamespace(context.Background(), "testsuite")

	// Initialize the metadata bucket via a real Prepare so storage.IDMap
	// works; otherwise getCleanupDirectories returns "bucket does not exist".
	_, err = s.Prepare(ctx, "tracked-key", "")
	require.NoError(t, err)

	snapshotsDir := filepath.Join(root, "snapshots")
	stale := filepath.Join(snapshotsDir, "9999")
	orphanAside := filepath.Join(snapshotsDir, ".orphan-9999-1700000000000000000")
	require.NoError(t, os.MkdirAll(stale, 0700))
	require.NoError(t, os.MkdirAll(orphanAside, 0700))

	cleaner, ok := s.(snapshots.Cleaner)
	require.True(t, ok, "snapshotter must implement snapshots.Cleaner")
	require.NoError(t, cleaner.Cleanup(ctx))

	_, err = os.Stat(stale)
	require.True(t, os.IsNotExist(err), "stale snapshot dir should be removed, got err=%v", err)
	_, err = os.Stat(orphanAside)
	require.True(t, os.IsNotExist(err), ".orphan-* dir should be removed, got err=%v", err)
}

// TestCreateSnapshotRenamesOrphanAside verifies that when Prepare finds a
// pre-existing directory at the target snapshot path on Windows, it renames
// the directory aside to .orphan-* instead of failing with "Access is denied".
func TestCreateSnapshotRenamesOrphanAside(t *testing.T) {
	root := t.TempDir()

	s, err := NewSnapshotter(root)
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })

	ctx := namespaces.WithNamespace(context.Background(), "testsuite")
	snapshotsDir := filepath.Join(root, "snapshots")

	// Seed a stale directory at every plausible next-ID path so that whichever
	// ID the metadata store allocates, createSnapshot has to rename it aside.
	for _, id := range []string{"1", "2", "3"} {
		stale := filepath.Join(snapshotsDir, id)
		require.NoError(t, os.MkdirAll(stale, 0700))
		require.NoError(t, os.WriteFile(filepath.Join(stale, "sentinel"), []byte(id), 0600))
	}

	_, err = s.Prepare(ctx, "snap-key", "")
	require.NoError(t, err)

	entries, err := os.ReadDir(snapshotsDir)
	require.NoError(t, err)

	var orphanSeen bool
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".orphan-") {
			orphanSeen = true
			break
		}
	}
	require.True(t, orphanSeen, "expected at least one .orphan-* dir after Prepare, got entries: %v", entries)
}
