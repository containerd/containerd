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

package lvm

import (
	"context"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/containerd/containerd/pkg/testutil"
	"github.com/containerd/containerd/snapshots"
	"github.com/containerd/containerd/snapshots/testsuite"
	"github.com/containerd/continuity/testutil/loopback"
	"gotest.tools/assert"
)

const (
	vgNamePrefix = "vgthin"
	lvPoolPrefix = "lvthinpool"
	loopbackSize = int64(10 << 30)
)

func TestLVMSnapshotterSuite(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Snapshotter only implemented for Linux")
	}

	testutil.RequiresRoot(t)

	testLvmSnapshotter := func(ctx context.Context, root string) (snapshots.Snapshotter, func() error, error) {
		var vgName string
		var lvPool string
		var err error
		//imagePath, loopDevice, err = createSparseDrive(t, root)
		loopDevice, loopCleanFn, err := loopback.New(loopbackSize)
		assert.NilError(t, err)
		//fmt.Printf("Using device %s", loopDevice)

		//suffix := randString(10)
		suffix := strconv.Itoa(time.Now().Nanosecond())

		vgName = vgNamePrefix + suffix
		lvPool = lvPoolPrefix + suffix

		output, err := createVolumeGroup(loopDevice, vgName)
		assert.NilError(t, err, output)

		output, err = toggleactivateVG(vgName, true)
		assert.NilError(t, err, output)

		output, err = createLogicalThinPool(vgName, lvPool)
		assert.NilError(t, err, output)

		config := &SnapConfig{
			VgName:   vgName,
			ThinPool: lvPool,
		}
		err = config.Validate(root)
		assert.NilError(t, err)

		snap, err := NewSnapshotter(ctx, config)
		assert.NilError(t, err)

		return snap, func() error {
			snap.Close()
			deleteVolumeGroup(vgName)
			loopCleanFn()
			return nil
		}, nil
	}

	testsuite.SnapshotterSuite(t, "LVM", testLvmSnapshotter)

}
