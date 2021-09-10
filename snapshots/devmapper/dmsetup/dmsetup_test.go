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

package dmsetup

import (
	"os"
	"strings"
	"testing"

	"github.com/docker/go-units"
	"golang.org/x/sys/unix"
	"gotest.tools/v3/assert"
	is "gotest.tools/v3/assert/cmp"

	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/pkg/testutil"
)

const (
	testPoolName   = "test-pool"
	testDeviceName = "test-device"
	deviceID       = 1
	snapshotID     = 2
)

func TestDMSetup(t *testing.T) {
	testutil.RequiresRoot(t)

	tempDir, err := os.MkdirTemp("", "dmsetup-tests-")
	assert.NilError(t, err, "failed to make temp dir for tests")

	defer func() {
		err := os.RemoveAll(tempDir)
		assert.NilError(t, err)
	}()

	dataImage, loopDataDevice := createLoopbackDevice(t, tempDir)
	metaImage, loopMetaDevice := createLoopbackDevice(t, tempDir)

	defer func() {
		err = mount.DetachLoopDevice(loopDataDevice, loopMetaDevice)
		assert.NilError(t, err, "failed to detach loop devices for data image: %s and meta image: %s", dataImage, metaImage)
	}()

	t.Run("CreatePool", func(t *testing.T) {
		err := CreatePool(testPoolName, loopDataDevice, loopMetaDevice, 128)
		assert.NilError(t, err, "failed to create thin-pool with %s %s", loopDataDevice, loopMetaDevice)

		table, err := Table(testPoolName)
		t.Logf("table: %s", table)
		assert.NilError(t, err)
		assert.Assert(t, strings.HasPrefix(table, "0 32768 thin-pool"))
		assert.Assert(t, strings.HasSuffix(table, "128 32768 1 skip_block_zeroing"))
	})

	t.Run("ReloadPool", func(t *testing.T) {
		err := ReloadPool(testPoolName, loopDataDevice, loopMetaDevice, 256)
		assert.NilError(t, err, "failed to reload thin-pool")
	})

	t.Run("CreateDevice", testCreateDevice)

	t.Run("CreateSnapshot", testCreateSnapshot)
	t.Run("DeleteSnapshot", testDeleteSnapshot)

	t.Run("ActivateDevice", testActivateDevice)
	t.Run("DeviceStatus", testDeviceStatus)
	t.Run("SuspendResumeDevice", testSuspendResumeDevice)
	t.Run("DiscardBlocks", testDiscardBlocks)
	t.Run("RemoveDevice", testRemoveDevice)

	t.Run("RemovePool", func(t *testing.T) {
		err = RemoveDevice(testPoolName, RemoveWithForce, RemoveWithRetries)
		assert.NilError(t, err, "failed to remove thin-pool")
	})

	t.Run("Version", testVersion)
}

func testCreateDevice(t *testing.T) {
	err := CreateDevice(testPoolName, deviceID)
	assert.NilError(t, err, "failed to create test device")

	err = CreateDevice(testPoolName, deviceID)
	assert.Assert(t, err == unix.EEXIST)

	infos, err := Info(testPoolName)
	assert.NilError(t, err)
	assert.Assert(t, is.Len(infos, 1), "got unexpected number of device infos")
}

func testCreateSnapshot(t *testing.T) {
	err := CreateSnapshot(testPoolName, snapshotID, deviceID)
	assert.NilError(t, err)
}

func testDeleteSnapshot(t *testing.T) {
	err := DeleteDevice(testPoolName, snapshotID)
	assert.NilError(t, err, "failed to send delete message")

	err = DeleteDevice(testPoolName, snapshotID)
	assert.Assert(t, err == unix.ENODATA)
}

func testActivateDevice(t *testing.T) {
	err := ActivateDevice(testPoolName, testDeviceName, 1, 1024, "")
	assert.NilError(t, err, "failed to activate device")

	err = ActivateDevice(testPoolName, testDeviceName, 1, 1024, "")
	assert.Equal(t, err, unix.EBUSY)

	if _, err := os.Stat("/dev/mapper/" + testDeviceName); err != nil && !os.IsExist(err) {
		assert.NilError(t, err, "failed to stat device")
	}

	list, err := Info(testPoolName)
	assert.NilError(t, err)
	assert.Assert(t, is.Len(list, 1))

	info := list[0]
	assert.Equal(t, testPoolName, info.Name)
	assert.Assert(t, info.TableLive)
}

func testDeviceStatus(t *testing.T) {
	status, err := Status(testDeviceName)
	assert.NilError(t, err)

	assert.Equal(t, int64(0), status.Offset)
	assert.Equal(t, int64(2), status.Length)
	assert.Equal(t, "thin", status.Target)
	assert.DeepEqual(t, status.Params, []string{"0", "-"})
}

func testSuspendResumeDevice(t *testing.T) {
	err := SuspendDevice(testDeviceName)
	assert.NilError(t, err)

	err = SuspendDevice(testDeviceName)
	assert.NilError(t, err)

	list, err := Info(testDeviceName)
	assert.NilError(t, err)
	assert.Assert(t, is.Len(list, 1))

	info := list[0]
	assert.Assert(t, info.Suspended)

	err = ResumeDevice(testDeviceName)
	assert.NilError(t, err)

	err = ResumeDevice(testDeviceName)
	assert.NilError(t, err)
}

func testDiscardBlocks(t *testing.T) {
	err := DiscardBlocks(testDeviceName)
	assert.NilError(t, err, "failed to discard blocks")
}

func testRemoveDevice(t *testing.T) {
	err := RemoveDevice(testPoolName)
	assert.Assert(t, err == unix.EBUSY, "removing thin-pool with dependencies shouldn't be allowed")

	err = RemoveDevice(testDeviceName, RemoveWithRetries)
	assert.NilError(t, err, "failed to remove thin-device")
}

func testVersion(t *testing.T) {
	version, err := Version()
	assert.NilError(t, err)
	assert.Assert(t, version != "")
}

func createLoopbackDevice(t *testing.T, dir string) (string, string) {
	file, err := os.CreateTemp(dir, "dmsetup-tests-")
	assert.NilError(t, err)

	size, err := units.RAMInBytes("16Mb")
	assert.NilError(t, err)

	err = file.Truncate(size)
	assert.NilError(t, err)

	err = file.Close()
	assert.NilError(t, err)

	imagePath := file.Name()

	loopDevice, err := mount.AttachLoopDevice(imagePath)
	assert.NilError(t, err)

	return imagePath, loopDevice
}
