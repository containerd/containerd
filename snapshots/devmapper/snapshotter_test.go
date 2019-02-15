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
	"testing"
	"time"

	"github.com/containerd/containerd/pkg/testutil"
	"github.com/containerd/containerd/snapshots"
	"github.com/containerd/containerd/snapshots/devmapper/dmsetup"
	"github.com/containerd/containerd/snapshots/devmapper/losetup"
	"github.com/containerd/containerd/snapshots/testsuite"
	"github.com/sirupsen/logrus"
	"gotest.tools/assert"
)

func TestSnapshotterSuite(t *testing.T) {
	testutil.RequiresRoot(t)

	logrus.SetLevel(logrus.DebugLevel)

	testsuite.SnapshotterSuite(t, "devmapper", func(ctx context.Context, root string) (snapshots.Snapshotter, func() error, error) {
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

		// Remove device mapper pool after test completes
		removePool := func() error {
			return snap.pool.RemovePool(ctx)
		}

		// Pool cleanup should be called before closing metadata store (as we need to retrieve device names)
		snap.cleanupFn = append([]closeFunc{removePool}, snap.cleanupFn...)

		return snap, func() error {
			err := snap.Close()
			assert.NilError(t, err, "failed to close snapshotter")

			err = losetup.DetachLoopDevice(loopDataDevice, loopMetaDevice)
			assert.NilError(t, err, "failed to detach loop devices")

			return err
		}, nil
	})
}
