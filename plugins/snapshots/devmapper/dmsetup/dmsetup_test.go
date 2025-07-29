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

package dmsetup

import (
	"os"
	"strings"
	"testing"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/pkg/testutil"
	"github.com/docker/go-units"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

const (
	testPoolName   = "test-pool"
	testDeviceName = "test-device"
	deviceID       = 1
	snapshotID     = 2
)

func TestDMSetup(t *testing.T) {
	testutil.RequiresRoot(t)

	tempDir := t.TempDir()

	dataImage, loopDataDevice := createLoopbackDevice(t, tempDir)
	metaImage, loopMetaDevice := createLoopbackDevice(t, tempDir)

	defer func() {
		err := mount.DetachLoopDevice(loopDataDevice, loopMetaDevice)
		require.NoError(t, err, "failed to detach loop devices for data image: %s and meta image: %s", dataImage, metaImage)
	}()

	t.Run("CreatePool", func(t *testing.T) {
		err := CreatePool(testPoolName, loopDataDevice, loopMetaDevice, 128)
		require.NoError(t, err, "failed to create thin-pool with %s %s", loopDataDevice, loopMetaDevice)

		table, err := Table(testPoolName)
		t.Logf("table: %s", table)
		require.NoError(t, err)
		assert.True(t, strings.HasPrefix(table, "0 32768 thin-pool"))
		assert.True(t, strings.HasSuffix(table, "128 32768 1 skip_block_zeroing"))
	})

	t.Run("ReloadPool", func(t *testing.T) {
		err := ReloadPool(testPoolName, loopDataDevice, loopMetaDevice, 256)
		require.NoError(t, err, "failed to reload thin-pool")
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
		err := RemoveDevice(testPoolName, RemoveWithForce, RemoveWithRetries)
		require.NoError(t, err, "failed to remove thin-pool")
	})

	t.Run("Version", testVersion)
}

func testCreateDevice(t *testing.T) {
	err := CreateDevice(testPoolName, deviceID)
	require.NoError(t, err, "failed to create test device")

	err = CreateDevice(testPoolName, deviceID)
	assert.Equal(t, unix.EEXIST, err)

	infos, err := Info(testPoolName)
	require.NoError(t, err)
	assert.Len(t, infos, 1, "got unexpected number of device infos")
}

func testCreateSnapshot(t *testing.T) {
	err := CreateSnapshot(testPoolName, snapshotID, deviceID)
	require.NoError(t, err)
}

func testDeleteSnapshot(t *testing.T) {
	err := DeleteDevice(testPoolName, snapshotID)
	require.NoError(t, err, "failed to send delete message")

	err = DeleteDevice(testPoolName, snapshotID)
	assert.Equal(t, unix.ENODATA, err)
}

func testActivateDevice(t *testing.T) {
	err := ActivateDevice(testPoolName, testDeviceName, 1, 1024, "")
	require.NoError(t, err, "failed to activate device")

	err = ActivateDevice(testPoolName, testDeviceName, 1, 1024, "")
	assert.Equal(t, unix.EBUSY, err)

	if _, err := os.Stat("/dev/mapper/" + testDeviceName); err != nil && !os.IsExist(err) {
		require.NoError(t, err, "failed to stat device")
	}

	list, err := Info(testPoolName)
	require.NoError(t, err)
	assert.Len(t, list, 1)

	info := list[0]
	assert.Equal(t, testPoolName, info.Name)
	assert.True(t, info.TableLive)
}

func testDeviceStatus(t *testing.T) {
	status, err := Status(testDeviceName)
	require.NoError(t, err)

	assert.Equal(t, int64(0), status.Offset)
	assert.Equal(t, int64(2), status.Length)
	assert.Equal(t, "thin", status.Target)
	assert.Equal(t, []string{"0", "-"}, status.Params)
}

func testSuspendResumeDevice(t *testing.T) {
	err := SuspendDevice(testDeviceName)
	require.NoError(t, err)

	err = SuspendDevice(testDeviceName)
	require.NoError(t, err)

	list, err := Info(testDeviceName)
	require.NoError(t, err)
	assert.Len(t, list, 1)

	info := list[0]
	assert.True(t, info.Suspended)

	err = ResumeDevice(testDeviceName)
	require.NoError(t, err)

	err = ResumeDevice(testDeviceName)
	require.NoError(t, err)
}

func testDiscardBlocks(t *testing.T) {
	err := DiscardBlocks(testDeviceName)
	require.NoError(t, err, "failed to discard blocks")
}

func testRemoveDevice(t *testing.T) {
	err := RemoveDevice(testPoolName)
	assert.Equal(t, unix.EBUSY, err, "removing thin-pool with dependencies shouldn't be allowed")

	err = RemoveDevice(testDeviceName, RemoveWithRetries)
	require.NoError(t, err, "failed to remove thin-device")
}

func testVersion(t *testing.T) {
	version, err := Version()
	require.NoError(t, err)
	assert.NotEmpty(t, version)
}

func createLoopbackDevice(t *testing.T, dir string) (string, string) {
	file, err := os.CreateTemp(dir, "dmsetup-tests-")
	require.NoError(t, err)
	t.Cleanup(func() {
		file.Close()
	})

	size, err := units.RAMInBytes("16Mb")
	require.NoError(t, err)

	err = file.Truncate(size)
	require.NoError(t, err)

	err = file.Close()
	require.NoError(t, err)

	imagePath := file.Name()

	loopDevice, err := mount.AttachLoopDevice(imagePath)
	require.NoError(t, err)

	return imagePath, loopDevice
}
