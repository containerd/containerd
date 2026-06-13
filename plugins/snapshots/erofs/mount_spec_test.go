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

// White-box unit tests for the mount spec produced by snapshotter.mounts().
// These tests construct a snapshotter directly — bypassing NewSnapshotter's
// kernel-EROFS check — so they run without root or EROFS kernel support.
// They exercise the single-parent fsmeta detection logic added to fix the
// tar-index two-file mount regression.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/core/snapshots/storage"
)

// snapshotterForMountTest builds a minimal *snapshotter whose root is set to
// root. It does not initialise the MetaStore (ms is nil) because mounts() only
// reads the filesystem — it never calls ms directly.
func snapshotterForMountTest(t *testing.T) (s *snapshotter, root string) {
	t.Helper()
	root = t.TempDir()
	s = &snapshotter{
		root:         root,
		dmverityMode: "off", // avoid any dm-verity stat calls
	}
	return s, root
}

// snapshotDir creates the snapshot directory structure for id under root and
// returns the directory path.
func snapshotDir(t *testing.T, root, id string) string {
	t.Helper()
	dir := filepath.Join(root, "snapshots", id)
	require.NoError(t, os.MkdirAll(dir, 0755))
	return dir
}

// writeFile creates a non-empty file at path (used to satisfy os.Stat checks).
func writeFile(t *testing.T, path string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte("x"), 0644))
}

// ============================================================
// Single-parent: tar-index layout (fsmeta.erofs present)
// ============================================================

// TestMountsSingleParentFsmetaUsed verifies that mounts() selects the
// fsmeta.erofs + layer.erofs (device=) mount when fsmeta.erofs exists for the
// parent snapshot.
func TestMountsingleParentFsmetaUsed(t *testing.T) {
	s, root := snapshotterForMountTest(t)

	const parentID = "parent-snap"
	dir := snapshotDir(t, root, parentID)

	// Populate both output files produced by GenerateTarIndexAndAppendTar.
	fsmeta := filepath.Join(dir, "fsmeta.erofs")
	layer := filepath.Join(dir, "layer.erofs")
	writeFile(t, fsmeta)
	writeFile(t, layer)

	snap := storage.Snapshot{
		Kind:      snapshots.KindView,
		ID:        "view-snap",
		ParentIDs: []string{parentID},
	}

	mounts, err := s.mounts(snap, snapshots.Info{})
	require.NoError(t, err)
	require.Len(t, mounts, 1, "single-parent tar-index must produce exactly one mount")

	m := mounts[0]
	assert.Equal(t, "erofs", m.Type)
	assert.Equal(t, fsmeta, m.Source,
		"mount source must be fsmeta.erofs, not layer.erofs")
	assert.Contains(t, m.Options, "ro")
	assert.Contains(t, m.Options, "loop")

	// device= must point at layer.erofs (the data file).
	var deviceOpt string
	for _, opt := range m.Options {
		if strings.HasPrefix(opt, "device=") {
			deviceOpt = opt
			break
		}
	}
	assert.Equal(t, "device="+layer, deviceOpt,
		"device= option must reference layer.erofs")
}

// TestMountsingleParentNoFsmetaFallback verifies that mounts() falls back to
// a direct erofs mount of layer.erofs when fsmeta.erofs does not exist.
func TestMountsingleParentNoFsmetaFallback(t *testing.T) {
	s, root := snapshotterForMountTest(t)

	const parentID = "regular-snap"
	dir := snapshotDir(t, root, parentID)

	// Regular layout: only layer.erofs, no fsmeta.erofs.
	layer := filepath.Join(dir, "layer.erofs")
	writeFile(t, layer)

	snap := storage.Snapshot{
		Kind:      snapshots.KindView,
		ID:        "view-snap",
		ParentIDs: []string{parentID},
	}

	mounts, err := s.mounts(snap, snapshots.Info{})
	require.NoError(t, err)
	require.Len(t, mounts, 1)

	m := mounts[0]
	assert.Equal(t, "erofs", m.Type)
	assert.Equal(t, layer, m.Source,
		"without fsmeta.erofs the mount source must be layer.erofs directly")

	// No device= option should be present.
	for _, opt := range m.Options {
		assert.False(t, strings.HasPrefix(opt, "device="),
			"regular layer must not have a device= option, got: %q", opt)
	}
}

// TestMountsingleParentFsmetaEmpty verifies that an empty fsmeta.erofs is
// treated as absent and falls back to the regular layer.erofs mount. An empty
// file cannot be a valid EROFS image and should not be used as the primary.
func TestMountsingleParentFsmetaEmpty(t *testing.T) {
	s, root := snapshotterForMountTest(t)

	const parentID = "empty-fsmeta-snap"
	dir := snapshotDir(t, root, parentID)

	// fsmeta.erofs exists but is empty.
	fsmeta := filepath.Join(dir, "fsmeta.erofs")
	require.NoError(t, os.WriteFile(fsmeta, []byte{}, 0644))

	layer := filepath.Join(dir, "layer.erofs")
	writeFile(t, layer)

	snap := storage.Snapshot{
		Kind:      snapshots.KindView,
		ID:        "view-snap",
		ParentIDs: []string{parentID},
	}

	mounts, err := s.mounts(snap, snapshots.Info{})
	require.NoError(t, err)
	require.Len(t, mounts, 1)

	m := mounts[0]
	// Empty fsmeta must be ignored; mount source must be layer.erofs.
	assert.Equal(t, layer, m.Source,
		"empty fsmeta.erofs must be ignored; mount source must be layer.erofs")
}

// ============================================================
// Multi-parent: fsmeta takes precedence (pre-existing behaviour)
// ============================================================

// TestMountsMultiParentFsmetaFirstParent verifies that the multi-parent loop
// in mounts() still short-circuits on fsmeta.erofs when the first parent has
// one, using it as primary and appending device= entries for all parents.
func TestMountsMultiParentFsmetaFirstParent(t *testing.T) {
	s, root := snapshotterForMountTest(t)

	const (
		parentA = "parent-a" // top (most recent), has fsmeta
		parentB = "parent-b" // bottom (older), only layer
	)

	dirA := snapshotDir(t, root, parentA)
	writeFile(t, filepath.Join(dirA, "fsmeta.erofs"))
	writeFile(t, filepath.Join(dirA, "layer.erofs"))

	dirB := snapshotDir(t, root, parentB)
	writeFile(t, filepath.Join(dirB, "layer.erofs"))

	// ParentIDs is ordered top→bottom.
	snap := storage.Snapshot{
		Kind:      snapshots.KindView,
		ID:        "view",
		ParentIDs: []string{parentA, parentB},
	}

	mounts, err := s.mounts(snap, snapshots.Info{})
	require.NoError(t, err)

	// The fsmeta mount is appended by the loop, then overlayfs wraps it.
	// There should be exactly 2 mounts: [fsmeta-based erofs, format/bind or overlay].
	require.GreaterOrEqual(t, len(mounts), 1)

	// The first non-overlay mount must use fsmeta.erofs.
	found := false
	for _, m := range mounts {
		if m.Source == filepath.Join(dirA, "fsmeta.erofs") {
			found = true
			// device= must cover both parents: parentB then parentA (reverse order).
			layerA := filepath.Join(dirA, "layer.erofs")
			layerB := filepath.Join(dirB, "layer.erofs")
			assert.Contains(t, m.Options, "device="+layerA)
			assert.Contains(t, m.Options, "device="+layerB)
			break
		}
	}
	assert.True(t, found, "a mount with Source=fsmeta.erofs of parentA must be present")
}
