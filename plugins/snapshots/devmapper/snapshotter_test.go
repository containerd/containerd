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

package devmapper

import (
	"context"
	_ "crypto/sha256"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/containerd/continuity/fs/fstest"
	"github.com/stretchr/testify/assert"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/core/snapshots/testsuite"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/containerd/v2/pkg/testutil"
	"github.com/containerd/containerd/v2/plugins/snapshots/devmapper/dmsetup"
	"github.com/containerd/log"
)

func TestSnapshotterSuite(t *testing.T) {
	testutil.RequiresRoot(t)

	assert.NoError(t, log.SetLevel("debug"))

	snapshotterFn := func(ctx context.Context, root string) (snapshots.Snapshotter, func() error, error) {
		poolName := fmt.Sprintf("containerd-snapshotter-suite-pool-%d", time.Now().Nanosecond())
		config := &Config{
			RootPath:      root,
			PoolName:      poolName,
			BaseImageSize: "16Mb",
		}
		return createSnapshotter(ctx, t, config)
	}

	testsuite.SnapshotterSuite(t, "devmapper", snapshotterFn)

	ctx := context.Background()
	ctx = namespaces.WithNamespace(ctx, "testsuite")

	t.Run("DevMapperUsage", func(t *testing.T) {
		snapshotter, closer, err := snapshotterFn(ctx, t.TempDir())
		assert.NoError(t, err)
		defer closer()

		testUsage(t, snapshotter)
	})
}

// testUsage tests devmapper's Usage implementation. This is an approximate test as it's hard to
// predict how many blocks will be consumed under different conditions and parameters.
func testUsage(t *testing.T, snapshotter snapshots.Snapshotter) {
	ctx := context.Background()

	// Create empty base layer
	_, err := snapshotter.Prepare(ctx, "prepare-1", "")
	assert.NoError(t, err)

	emptyLayerUsage, err := snapshotter.Usage(ctx, "prepare-1")
	assert.NoError(t, err)

	// Should be > 0 as just written file system also consumes blocks
	assert.Greater(t, emptyLayerUsage.Size, int64(0))

	err = snapshotter.Commit(ctx, "layer-1", "prepare-1")
	assert.NoError(t, err)

	// Create child layer with 1MB file

	var (
		sizeBytes   int64 = 1048576 // 1MB
		baseApplier       = fstest.Apply(fstest.CreateRandomFile("/a", 12345679, sizeBytes, 0777))
	)

	mounts, err := snapshotter.Prepare(ctx, "prepare-2", "layer-1")
	assert.NoError(t, err)

	err = mount.WithTempMount(ctx, mounts, baseApplier.Apply)
	assert.NoError(t, err)

	err = snapshotter.Commit(ctx, "layer-2", "prepare-2")
	assert.NoError(t, err)

	layer2Usage, err := snapshotter.Usage(ctx, "layer-2")
	assert.NoError(t, err)

	// Should be at least 1 MB + fs metadata
	assert.GreaterOrEqual(t, layer2Usage.Size, sizeBytes,
		"%d > %d", layer2Usage.Size, sizeBytes)
}

func TestMkfsExt4(t *testing.T) {
	ctx := context.Background()
	// We test the default setting which is lazy init is disabled
	err := mkfs(ctx, "ext4", "nodiscard,lazy_itable_init=0,lazy_journal_init=0", "")
	assert.Contains(t, err.Error(), `mkfs.ext4 couldn't initialize ""`)
}

func TestMkfsExt4NonDefault(t *testing.T) {
	ctx := context.Background()
	// We test a non default setting where we enable lazy init for ext4
	err := mkfs(ctx, "ext4", "nodiscard", "")
	assert.Contains(t, err.Error(), `mkfs.ext4 couldn't initialize ""`)
}

func TestMkfsXfs(t *testing.T) {
	ctx := context.Background()
	err := mkfs(ctx, "xfs", "", "")
	assert.Contains(t, err.Error(), `mkfs.xfs couldn't initialize ""`)
}

func TestMkfsXfsNonDefault(t *testing.T) {
	ctx := context.Background()
	err := mkfs(ctx, "xfs", "noquota", "")
	assert.Contains(t, err.Error(), `mkfs.xfs couldn't initialize ""`)
}

func TestMultipleXfsMounts(t *testing.T) {
	testutil.RequiresRoot(t)

	assert.NoError(t, log.SetLevel("debug"))

	ctx := context.Background()
	ctx = namespaces.WithNamespace(ctx, "testsuite")

	poolName := fmt.Sprintf("containerd-snapshotter-suite-pool-%d", time.Now().Nanosecond())
	config := &Config{
		RootPath: t.TempDir(),
		PoolName: poolName,
		// Size for xfs volume is kept at 300Mb because xfsprogs 5.19.0 (>=ubuntu 24.04) enforces a minimum volume size
		BaseImageSize:  "300Mb",
		FileSystemType: "xfs",
	}
	snapshotter, closer, err := createSnapshotter(ctx, t, config)
	assert.NoError(t, err)
	defer closer()

	var (
		sizeBytes   int64 = 1048576 // 1MB
		baseApplier       = fstest.Apply(fstest.CreateRandomFile("/a", 12345679, sizeBytes, 0777))
	)

	// Create base layer
	mounts, err := snapshotter.Prepare(ctx, "prepare-1", "")
	assert.NoError(t, err)

	root1 := t.TempDir()
	defer func() {
		mount.UnmountAll(root1, 0)
	}()
	err = mount.All(mounts, root1)
	assert.NoError(t, err)
	baseApplier.Apply(root1)
	snapshotter.Commit(ctx, "layer-1", "prepare-1")

	// Create one child layer
	mounts, err = snapshotter.Prepare(ctx, "prepare-2", "layer-1")
	assert.NoError(t, err)

	root2 := t.TempDir()
	defer func() {
		mount.UnmountAll(root2, 0)
	}()
	err = mount.All(mounts, root2)
	assert.NoError(t, err)
}

func createSnapshotter(ctx context.Context, t *testing.T, config *Config) (snapshots.Snapshotter, func() error, error) {
	// Create loopback devices for each test case
	_, loopDataDevice := createLoopbackDevice(t, config.RootPath)
	_, loopMetaDevice := createLoopbackDevice(t, config.RootPath)

	err := dmsetup.CreatePool(config.PoolName, loopDataDevice, loopMetaDevice, 64*1024/dmsetup.SectorSize)
	assert.Nil(t, err, "failed to create pool %q", config.PoolName)

	snap, err := NewSnapshotter(ctx, config)
	if err != nil {
		return nil, nil, err
	}

	// Remove device mapper pool and detach loop devices after test completes
	removePool := func() error {
		result := errors.Join(
			snap.pool.RemovePool(ctx),
			mount.DetachLoopDevice(loopDataDevice, loopMetaDevice))

		return result
	}

	// Pool cleanup should be called before closing metadata store (as we need to retrieve device names)
	snap.cleanupFn = append([]closeFunc{removePool}, snap.cleanupFn...)

	return snap, snap.Close, nil
}
