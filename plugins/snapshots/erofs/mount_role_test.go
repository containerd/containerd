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

// White-box unit tests for the mounts produced for chains with device and
// overlay-data role layers (see the EROFS image layer format specification,
// https://github.com/erofs/erofs-image-spec, §2.4 and §3.8). Like
// mount_spec_test.go, they run without root or EROFS kernel support.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/containerd/errdefs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/core/snapshots/storage"
	"github.com/containerd/containerd/v2/internal/dmverity"
	"github.com/containerd/containerd/v2/internal/erofsutils"
)

// writeRoleLayer creates the blob a differ writes for a layer of the given
// role (erofsutils.DataBlobName or erofsutils.DeviceBlobName) and returns its
// path.
func writeRoleLayer(t *testing.T, root, id, name string) string {
	t.Helper()
	dir := filepath.Join(root, "snapshots", id)
	require.NoError(t, os.MkdirAll(dir, 0755))
	blob := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(blob, []byte("x"), 0644))
	return blob
}

// TestMountsDeviceRole verifies that device role layers are attached, bottom
// to top, to the nearest layer above them and are not mounted themselves.
func TestMountsDeviceRole(t *testing.T) {
	s, root := snapshotterForMountTest(t)
	// Manifest order: base, dev1, dev2, meta.
	metaBlob, _ := writeLayer(t, root, "meta", 0)
	dev2 := writeRoleLayer(t, root, "dev2", erofsutils.DeviceBlobName)
	dev1 := writeRoleLayer(t, root, "dev1", erofsutils.DeviceBlobName)
	baseBlob, _ := writeLayer(t, root, "base", 0)

	snap := storage.Snapshot{Kind: snapshots.KindView, ID: "view", ParentIDs: []string{"meta", "dev2", "dev1", "base"}}
	mounts, err := s.mounts(snap, snapshots.Info{}, make([]snapshots.Info, 4))
	require.NoError(t, err)
	require.Len(t, mounts, 3)
	assert.Equal(t, metaBlob, mounts[0].Source)
	assert.Equal(t, []string{"ro", "loop", "device=" + dev1, "device=" + dev2}, mounts[0].Options)
	assert.Equal(t, baseBlob, mounts[1].Source)
	assert.Equal(t, []string{"ro", "loop"}, mounts[1].Options)
	assert.Equal(t, "format/mkdir/overlay", mounts[2].Type)
	assert.Contains(t, mounts[2].Options, "lowerdir={{ overlay 0 1 }}")
}

// TestMountsDeviceRoleDmverity verifies that a device role layer's dm-verity
// metadata is passed by its device index on the consuming layer's mount.
func TestMountsDeviceRoleDmverity(t *testing.T) {
	s, root := snapshotterForMountTest(t)
	s.dmverityMode = "auto"
	metaBlob, _ := writeLayer(t, root, "meta", 0)
	dev := writeRoleLayer(t, root, "dev", erofsutils.DeviceBlobName)
	require.NoError(t, os.WriteFile(dmverity.MetadataPath(dev), []byte("{}"), 0644))
	writeLayer(t, root, "base", 0)

	snap := storage.Snapshot{Kind: snapshots.KindView, ID: "view", ParentIDs: []string{"meta", "dev", "base"}}
	mounts, err := s.mounts(snap, snapshots.Info{}, make([]snapshots.Info, 3))
	require.NoError(t, err)
	require.Len(t, mounts, 3)
	assert.Equal(t, metaBlob, mounts[0].Source)
	assert.Equal(t, []string{
		"ro", "loop",
		"device=" + dev,
		dmverity.DeviceMountOption + "0=" + dmverity.MetadataPath(dev),
	}, mounts[0].Options)
}

// TestMountsDeviceRoleWithOwnDevices verifies that a layer with data devices of
// its own cannot also consume a device role layer: its image numbers devices
// from one source only.
func TestMountsDeviceRoleWithOwnDevices(t *testing.T) {
	s, root := snapshotterForMountTest(t)
	writeLayer(t, root, "meta", 1)
	writeRoleLayer(t, root, "dev", erofsutils.DeviceBlobName)
	writeLayer(t, root, "base", 0)

	snap := storage.Snapshot{Kind: snapshots.KindView, ID: "view", ParentIDs: []string{"meta", "dev", "base"}}
	_, err := s.mounts(snap, snapshots.Info{}, make([]snapshots.Info, 3))
	assert.ErrorIs(t, err, errdefs.ErrFailedPrecondition)
}

// TestMountsUnconsumedDeviceRole verifies that a device role layer at the top
// of the parents is left out: the snapshot itself consumes it.
func TestMountsUnconsumedDeviceRole(t *testing.T) {
	s, root := snapshotterForMountTest(t)
	writeRoleLayer(t, root, "dev", erofsutils.DeviceBlobName)
	baseBlob, _ := writeLayer(t, root, "base", 0)

	snap := storage.Snapshot{Kind: snapshots.KindActive, ID: "active", ParentIDs: []string{"dev", "base"}}
	mounts, err := s.mounts(snap, snapshots.Info{}, make([]snapshots.Info, 2))
	require.NoError(t, err)
	require.Len(t, mounts, 2)
	assert.Equal(t, baseBlob, mounts[0].Source)
	assert.Equal(t, []string{"ro", "loop"}, mounts[0].Options)
	assert.Contains(t, mounts[1].Options, "lowerdir={{ mount 0 }}")
	assert.Contains(t, mounts[1].Options, "upperdir="+s.upperPath("active"))
}

// TestMountsOnlyRoleParents verifies that an active snapshot whose parents are
// all consumed by its own layer mounts only its own content, and that a view
// of such parents is refused.
func TestMountsOnlyRoleParents(t *testing.T) {
	s, root := snapshotterForMountTest(t)
	writeRoleLayer(t, root, "dev", erofsutils.DeviceBlobName)
	writeRoleLayer(t, root, "data", erofsutils.DataBlobName)

	snap := storage.Snapshot{Kind: snapshots.KindActive, ID: "active", ParentIDs: []string{"dev", "data"}}
	mounts, err := s.mounts(snap, snapshots.Info{}, make([]snapshots.Info, 2))
	require.NoError(t, err)
	require.Len(t, mounts, 1)
	assert.Equal(t, "bind", mounts[0].Type)
	assert.Equal(t, s.upperPath("active"), mounts[0].Source)
	assert.Equal(t, []string{"rw", "rbind"}, mounts[0].Options)

	snap = storage.Snapshot{Kind: snapshots.KindView, ID: "view", ParentIDs: []string{"dev", "data"}}
	_, err = s.mounts(snap, snapshots.Info{}, make([]snapshots.Info, 2))
	assert.ErrorIs(t, err, errdefs.ErrFailedPrecondition)

	snap = storage.Snapshot{Kind: snapshots.KindView, ID: "view", ParentIDs: []string{"dev"}}
	_, err = s.mounts(snap, snapshots.Info{}, make([]snapshots.Info, 1))
	assert.ErrorIs(t, err, errdefs.ErrFailedPrecondition)
}

// TestMountsOverlayDataRole verifies that overlay-data role layers are mounted
// as data-only lowers after the regular lowers, with metacopy enabled.
func TestMountsOverlayDataRole(t *testing.T) {
	s, root := snapshotterForMountTest(t)
	metaBlob, _ := writeLayer(t, root, "meta", 0)
	data := writeRoleLayer(t, root, "data", erofsutils.DataBlobName)

	snap := storage.Snapshot{Kind: snapshots.KindView, ID: "view", ParentIDs: []string{"meta", "data"}}
	mounts, err := s.mounts(snap, snapshots.Info{}, make([]snapshots.Info, 2))
	require.NoError(t, err)
	require.Len(t, mounts, 3)
	assert.Equal(t, metaBlob, mounts[0].Source)
	assert.Equal(t, data, mounts[1].Source)
	assert.Equal(t, "erofs", mounts[1].Type)
	assert.Contains(t, mounts[2].Options, "lowerdir={{ mount 0 }}::{{ mount 1 }}")
	assert.Contains(t, mounts[2].Options, "metacopy=on")
}

// TestMountsOverlayDataRoleBlockMode verifies the data-only lowers follow the
// writable block image and the regular lowers in block mode.
func TestMountsOverlayDataRoleBlockMode(t *testing.T) {
	s, root := snapshotterForMountTest(t)
	s.blockMode = true
	s.defaultWritable = 1 << 20
	writeLayer(t, root, "meta", 0)
	writeLayer(t, root, "base", 0)
	data := writeRoleLayer(t, root, "data", erofsutils.DataBlobName)

	snap := storage.Snapshot{Kind: snapshots.KindActive, ID: "active", ParentIDs: []string{"meta", "data", "base"}}
	mounts, err := s.mounts(snap, snapshots.Info{}, make([]snapshots.Info, 3))
	require.NoError(t, err)
	require.Len(t, mounts, 5)
	assert.Equal(t, "mkfs/ext4", mounts[0].Type)
	assert.Equal(t, data, mounts[3].Source)
	assert.Contains(t, mounts[4].Options, "lowerdir={{ overlay 1 2 }}::{{ mount 3 }}")
}

// TestMountsMergedFsmetaSkippedForRoles verifies that a merged fsmeta is not
// used when a layer it covers has a composition role.
func TestMountsMergedFsmetaSkippedForRoles(t *testing.T) {
	s, root := snapshotterForMountTest(t)
	metaBlob, _ := writeLayer(t, root, "meta", 0)
	dev := writeRoleLayer(t, root, "dev", erofsutils.DeviceBlobName)
	writeLayer(t, root, "base", 0)
	require.NoError(t, os.WriteFile(s.fsMetaPath("meta"), []byte("merged"), 0644))

	snap := storage.Snapshot{Kind: snapshots.KindView, ID: "view", ParentIDs: []string{"meta", "dev", "base"}}
	mounts, err := s.mounts(snap, snapshots.Info{}, make([]snapshots.Info, 3))
	require.NoError(t, err)
	require.Len(t, mounts, 3)
	assert.Equal(t, metaBlob, mounts[0].Source)
	assert.Contains(t, mounts[0].Options, "device="+dev)
}
