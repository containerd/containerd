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

// White-box unit tests for the mounts produced for layers whose blob has data
// devices. They construct a snapshotter directly, bypassing NewSnapshotter's
// kernel-EROFS check, so they run without root or EROFS kernel support.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/core/snapshots/storage"
	"github.com/containerd/containerd/v2/internal/dmverity"
	"github.com/containerd/containerd/v2/internal/erofsutils"
)

// snapshotterForMountTest builds a minimal *snapshotter rooted in a temporary
// directory. It has no MetaStore: mounts() only reads the filesystem.
func snapshotterForMountTest(t *testing.T) (*snapshotter, string) {
	t.Helper()
	root := t.TempDir()
	return &snapshotter{root: root, dmverityMode: "off"}, root
}

// writeLayer creates a layer blob for snapshot id with the given number of
// data devices and returns the blob path and device paths.
func writeLayer(t *testing.T, root, id string, devices int) (string, []string) {
	t.Helper()
	dir := filepath.Join(root, "snapshots", id)
	require.NoError(t, os.MkdirAll(dir, 0755))
	blob := filepath.Join(dir, "layer.erofs")
	require.NoError(t, os.WriteFile(blob, []byte("x"), 0644))
	var paths []string
	for n := range devices {
		p := erofsutils.DevicePath(dir, n)
		require.NoError(t, os.WriteFile(p, []byte("x"), 0644))
		paths = append(paths, p)
	}
	return blob, paths
}

// TestMountsSingleParentWithDevice verifies that a view of a layer with a data
// device mounts the layer blob with the device attached.
func TestMountsSingleParentWithDevice(t *testing.T) {
	s, root := snapshotterForMountTest(t)
	blob, devices := writeLayer(t, root, "p0", 1)

	snap := storage.Snapshot{Kind: snapshots.KindView, ID: "view", ParentIDs: []string{"p0"}}
	mounts, err := s.mounts(snap, snapshots.Info{}, make([]snapshots.Info, 1))
	require.NoError(t, err)
	require.Len(t, mounts, 1)
	assert.Equal(t, "erofs", mounts[0].Type)
	assert.Equal(t, blob, mounts[0].Source)
	assert.Equal(t, []string{"ro", "loop", "device=" + devices[0]}, mounts[0].Options)
}

// TestMountsMultiParentWithDevices verifies that each overlay lower carries its
// own data devices, in device order.
func TestMountsMultiParentWithDevices(t *testing.T) {
	s, root := snapshotterForMountTest(t)
	topBlob, topDevices := writeLayer(t, root, "p0", 2)
	bottomBlob, _ := writeLayer(t, root, "p1", 0)

	snap := storage.Snapshot{Kind: snapshots.KindView, ID: "view", ParentIDs: []string{"p0", "p1"}}
	mounts, err := s.mounts(snap, snapshots.Info{}, make([]snapshots.Info, 2))
	require.NoError(t, err)
	require.Len(t, mounts, 3)
	assert.Equal(t, topBlob, mounts[0].Source)
	assert.Equal(t, []string{"ro", "loop", "device=" + topDevices[0], "device=" + topDevices[1]}, mounts[0].Options)
	assert.Equal(t, bottomBlob, mounts[1].Source)
	assert.Equal(t, []string{"ro", "loop"}, mounts[1].Options)
}

// TestMountsMergedFsmetaSkippedForDevices verifies that a merged fsmeta is not
// used when a layer it covers has data devices: the fsmeta addresses each
// layer as one device, which such a layer does not fit.
func TestMountsMergedFsmetaSkippedForDevices(t *testing.T) {
	s, root := snapshotterForMountTest(t)
	topBlob, _ := writeLayer(t, root, "p0", 0)
	bottomBlob, bottomDevices := writeLayer(t, root, "p1", 1)
	require.NoError(t, os.WriteFile(s.fsMetaPath("p0"), []byte("merged"), 0644))

	snap := storage.Snapshot{Kind: snapshots.KindView, ID: "view", ParentIDs: []string{"p0", "p1"}}
	mounts, err := s.mounts(snap, snapshots.Info{}, make([]snapshots.Info, 2))
	require.NoError(t, err)
	require.Len(t, mounts, 3)
	assert.Equal(t, topBlob, mounts[0].Source)
	assert.Equal(t, bottomBlob, mounts[1].Source)
	assert.Contains(t, mounts[1].Options, "device="+bottomDevices[0])
}

// TestCreateErofsMountDeviceDmverity verifies that dm-verity metadata for a data
// device is passed by device index, and that mode "on" requires it for every
// device.
func TestCreateErofsMountDeviceDmverity(t *testing.T) {
	s, root := snapshotterForMountTest(t)
	blob, devices := writeLayer(t, root, "p0", 2)
	for _, p := range []string{blob, devices[1]} {
		require.NoError(t, os.WriteFile(dmverity.MetadataPath(p), []byte("{}"), 0644))
	}

	s.dmverityMode = "auto"
	m, err := s.createErofsMount(blob, devices)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"ro", "loop",
		dmverity.MountOption + dmverity.MetadataPath(blob),
		"device=" + devices[0],
		"device=" + devices[1],
		dmverity.DeviceMountOption + "1=" + dmverity.MetadataPath(devices[1]),
	}, m.Options)

	s.dmverityMode = "on"
	_, err = s.createErofsMount(blob, devices)
	require.Error(t, err, "mode on must reject a data device without dm-verity metadata")

	s.dmverityMode = "off"
	m, err = s.createErofsMount(blob, devices)
	require.NoError(t, err)
	assert.Equal(t, []string{"ro", "loop", "device=" + devices[0], "device=" + devices[1]}, m.Options)
}
