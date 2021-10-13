//go:build linux
// +build linux

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
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/containerd/continuity/fs/fstest"
	"github.com/hashicorp/go-multierror"
	"github.com/sirupsen/logrus"
	"gotest.tools/v3/assert"

	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/pkg/testutil"
	"github.com/containerd/containerd/snapshots"
	"github.com/containerd/containerd/snapshots/devmapper/dmsetup"
	"github.com/containerd/containerd/snapshots/testsuite"
)

func TestSnapshotterSuite(t *testing.T) {
	testutil.RequiresRoot(t)

	logrus.SetLevel(logrus.DebugLevel)

	snapshotterFn := func(ctx context.Context, root string) (snapshots.Snapshotter, func() error, error) {
		// Create loopback devices for each test case
		_, loopDataDevice := createLoopbackDevice(t, root)
		_, loopMetaDevice := createLoopbackDevice(t, root)

		poolName := fmt.Sprintf("containerd-snapshotter-suite-pool-%d", time.Now().Nanosecond())
		err := dmsetup.CreatePool(poolName, loopDataDevice, loopMetaDevice, 64*1024/dmsetup.SectorSize)
		assert.NilError(t, err, "failed to create pool %q", poolName)

		config := &Config{
			RootPath:      root,
			PoolName:      poolName,
			BaseImageSize: "16Mb",
		}

		snap, err := NewSnapshotter(context.Background(), config)
		if err != nil {
			return nil, nil, err
		}

		// Remove device mapper pool and detach loop devices after test completes
		removePool := func() error {
			result := multierror.Append(
				snap.pool.RemovePool(ctx),
				mount.DetachLoopDevice(loopDataDevice, loopMetaDevice))

			return result.ErrorOrNil()
		}

		// Pool cleanup should be called before closing metadata store (as we need to retrieve device names)
		snap.cleanupFn = append([]closeFunc{removePool}, snap.cleanupFn...)

		return snap, snap.Close, nil
	}

	testsuite.SnapshotterSuite(t, "devmapper", snapshotterFn)

	ctx := context.Background()
	ctx = namespaces.WithNamespace(ctx, "testsuite")

	t.Run("DevMapperUsage", func(t *testing.T) {
		tempDir, err := os.MkdirTemp("", "snapshot-suite-usage")
		assert.NilError(t, err)
		defer os.RemoveAll(tempDir)

		snapshotter, closer, err := snapshotterFn(ctx, tempDir)
		assert.NilError(t, err)
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
	assert.NilError(t, err)

	emptyLayerUsage, err := snapshotter.Usage(ctx, "prepare-1")
	assert.NilError(t, err)

	// Should be > 0 as just written file system also consumes blocks
	assert.Assert(t, emptyLayerUsage.Size > 0)

	err = snapshotter.Commit(ctx, "layer-1", "prepare-1")
	assert.NilError(t, err)

	// Create child layer with 1MB file

	var (
		sizeBytes   int64 = 1048576 // 1MB
		baseApplier       = fstest.Apply(fstest.CreateRandomFile("/a", 12345679, sizeBytes, 0777))
	)

	mounts, err := snapshotter.Prepare(ctx, "prepare-2", "layer-1")
	assert.NilError(t, err)

	err = mount.WithTempMount(ctx, mounts, baseApplier.Apply)
	assert.NilError(t, err)

	err = snapshotter.Commit(ctx, "layer-2", "prepare-2")
	assert.NilError(t, err)

	layer2Usage, err := snapshotter.Usage(ctx, "layer-2")
	assert.NilError(t, err)

	// Should be at least 1 MB + fs metadata
	assert.Check(t, layer2Usage.Size >= sizeBytes,
		"%d > %d", layer2Usage.Size, sizeBytes)
}

func TestMkfsExt4(t *testing.T) {
	ctx := context.Background()
	// We test the default setting which is lazy init is disabled
	err := mkfs(ctx, "ext4", "nodiscard,lazy_itable_init=0,lazy_journal_init=0", "")
	assert.ErrorContains(t, err, `mkfs.ext4 couldn't initialize ""`)
}

func TestMkfsExt4NonDefault(t *testing.T) {
	ctx := context.Background()
	// We test a non default setting where we enable lazy init for ext4
	err := mkfs(ctx, "ext4", "nodiscard", "")
	assert.ErrorContains(t, err, `mkfs.ext4 couldn't initialize ""`)
}

func TestMkfsXfs(t *testing.T) {
	ctx := context.Background()
	err := mkfs(ctx, "xfs", "", "")
	assert.ErrorContains(t, err, `mkfs.xfs couldn't initialize ""`)
}

func TestMkfsXfsNonDefault(t *testing.T) {
	ctx := context.Background()
	err := mkfs(ctx, "xfs", "noquota", "")
	assert.ErrorContains(t, err, `mkfs.xfs couldn't initialize ""`)
}
