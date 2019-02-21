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
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/containerd/containerd/pkg/testutil"
	"github.com/containerd/containerd/snapshots/devmapper/dmsetup"
	"github.com/containerd/containerd/snapshots/devmapper/losetup"
	"github.com/docker/go-units"
	"github.com/sirupsen/logrus"
	"gotest.tools/assert"
)

const (
	thinDevice1 = "thin-1"
	thinDevice2 = "thin-2"
	snapDevice1 = "snap-1"
	device1Size = 100000
	device2Size = 200000
	testsPrefix = "devmapper-snapshotter-tests-"
)

// TestPoolDevice runs integration tests for pool device.
// The following scenario implemented:
// - Create pool device with name 'test-pool-device'
// - Create two thin volumes 'thin-1' and 'thin-2'
// - Write ext4 file system on 'thin-1' and make sure it'errs moutable
// - Write v1 test file on 'thin-1' volume
// - Take 'thin-1' snapshot 'snap-1'
// - Change v1 file to v2 on 'thin-1'
// - Mount 'snap-1' and make sure test file is v1
// - Unmount volumes and remove all devices
func TestPoolDevice(t *testing.T) {
	testutil.RequiresRoot(t)

	logrus.SetLevel(logrus.DebugLevel)
	ctx := context.Background()

	tempDir, err := ioutil.TempDir("", "pool-device-test-")
	assert.NilError(t, err, "couldn't get temp directory for testing")

	_, loopDataDevice := createLoopbackDevice(t, tempDir)
	_, loopMetaDevice := createLoopbackDevice(t, tempDir)

	defer func() {
		// Detach loop devices and remove images
		err := losetup.DetachLoopDevice(loopDataDevice, loopMetaDevice)
		assert.NilError(t, err)

		err = os.RemoveAll(tempDir)
		assert.NilError(t, err, "couldn't cleanup temp directory")
	}()

	config := &Config{
		PoolName:             "test-pool-device-1",
		RootPath:             tempDir,
		DataDevice:           loopDataDevice,
		MetadataDevice:       loopMetaDevice,
		DataBlockSize:        "65536",
		DataBlockSizeSectors: 128,
		BaseImageSize:        "16mb",
		BaseImageSizeBytes:   16 * 1024 * 1024,
	}

	pool, err := NewPoolDevice(ctx, config)
	assert.NilError(t, err, "can't create device pool")
	assert.Assert(t, pool != nil)

	defer func() {
		err := pool.RemovePool(ctx)
		assert.NilError(t, err, "can't close device pool")
	}()

	// Create thin devices
	t.Run("CreateThinDevice", func(t *testing.T) {
		testCreateThinDevice(t, pool)
	})

	// Make ext4 filesystem on 'thin-1'
	t.Run("MakeFileSystem", func(t *testing.T) {
		testMakeFileSystem(t, pool)
	})

	// Mount 'thin-1'
	thin1MountPath := tempMountPath(t)
	output, err := exec.Command("mount", dmsetup.GetFullDevicePath(thinDevice1), thin1MountPath).CombinedOutput()
	assert.NilError(t, err, "failed to mount '%s': %s", thinDevice1, string(output))

	// Write v1 test file on 'thin-1' device
	thin1TestFilePath := filepath.Join(thin1MountPath, "TEST")
	err = ioutil.WriteFile(thin1TestFilePath, []byte("test file (v1)"), 0700)
	assert.NilError(t, err, "failed to write test file v1 on '%s' volume", thinDevice1)

	// Take snapshot of 'thin-1'
	t.Run("CreateSnapshotDevice", func(t *testing.T) {
		testCreateSnapshot(t, pool)
	})

	// Update TEST file on 'thin-1' to v2
	err = ioutil.WriteFile(thin1TestFilePath, []byte("test file (v2)"), 0700)
	assert.NilError(t, err, "failed to write test file v2 on 'thin-1' volume after taking snapshot")

	// Mount 'snap-1' and make sure TEST file is v1
	snap1MountPath := tempMountPath(t)
	output, err = exec.Command("mount", dmsetup.GetFullDevicePath(snapDevice1), snap1MountPath).CombinedOutput()
	assert.NilError(t, err, "failed to mount '%s' device: %s", snapDevice1, string(output))

	// Read test file from snapshot device and make sure it's v1
	fileData, err := ioutil.ReadFile(filepath.Join(snap1MountPath, "TEST"))
	assert.NilError(t, err, "couldn't read test file from '%s' device", snapDevice1)
	assert.Assert(t, string(fileData) == "test file (v1)", "test file content is invalid on snapshot")

	// Unmount devices before removing
	output, err = exec.Command("umount", thin1MountPath, snap1MountPath).CombinedOutput()
	assert.NilError(t, err, "failed to unmount devices: %s", string(output))

	t.Run("DeactivateDevice", func(t *testing.T) {
		testDeactivateThinDevice(t, pool)
	})

	t.Run("RemoveDevice", func(t *testing.T) {
		testRemoveThinDevice(t, pool)
	})
}

func testCreateThinDevice(t *testing.T, pool *PoolDevice) {
	ctx := context.Background()

	err := pool.CreateThinDevice(ctx, thinDevice1, device1Size)
	assert.NilError(t, err, "can't create first thin device")

	err = pool.CreateThinDevice(ctx, thinDevice1, device1Size)
	assert.Assert(t, err != nil, "device pool allows duplicated device names")

	err = pool.CreateThinDevice(ctx, thinDevice2, device2Size)
	assert.NilError(t, err, "can't create second thin device")

	deviceInfo1, err := pool.metadata.GetDevice(ctx, thinDevice1)
	assert.NilError(t, err)

	deviceInfo2, err := pool.metadata.GetDevice(ctx, thinDevice2)
	assert.NilError(t, err)

	assert.Assert(t, deviceInfo1.DeviceID != deviceInfo2.DeviceID, "assigned device ids should be different")
}

func testMakeFileSystem(t *testing.T, pool *PoolDevice) {
	devicePath := dmsetup.GetFullDevicePath(thinDevice1)
	args := []string{
		devicePath,
		"-E",
		"nodiscard,lazy_itable_init=0,lazy_journal_init=0",
	}

	output, err := exec.Command("mkfs.ext4", args...).CombinedOutput()
	assert.NilError(t, err, "failed to make filesystem on '%s': %s", thinDevice1, string(output))
}

func testCreateSnapshot(t *testing.T, pool *PoolDevice) {
	err := pool.CreateSnapshotDevice(context.Background(), thinDevice1, snapDevice1, device1Size)
	assert.NilError(t, err, "failed to create snapshot from '%s' volume", thinDevice1)
}

func testDeactivateThinDevice(t *testing.T, pool *PoolDevice) {
	deviceList := []string{
		thinDevice2,
		snapDevice1,
	}

	for _, deviceName := range deviceList {
		assert.Assert(t, pool.IsActivated(deviceName))

		err := pool.DeactivateDevice(context.Background(), deviceName, false)
		assert.NilError(t, err, "failed to remove '%s'", deviceName)

		assert.Assert(t, !pool.IsActivated(deviceName))
	}
}

func testRemoveThinDevice(t *testing.T, pool *PoolDevice) {
	err := pool.RemoveDevice(testCtx, thinDevice1)
	assert.NilError(t, err, "should delete thin device from pool")
}

func tempMountPath(t *testing.T) string {
	path, err := ioutil.TempDir("", "devmapper-snapshotter-mount-")
	assert.NilError(t, err, "failed to get temp directory for mount")

	return path
}

func createLoopbackDevice(t *testing.T, dir string) (string, string) {
	file, err := ioutil.TempFile(dir, testsPrefix)
	assert.NilError(t, err)

	size, err := units.RAMInBytes("128Mb")
	assert.NilError(t, err)

	err = file.Truncate(size)
	assert.NilError(t, err)

	err = file.Close()
	assert.NilError(t, err)

	imagePath := file.Name()

	loopDevice, err := losetup.AttachLoopDevice(imagePath)
	assert.NilError(t, err)

	return imagePath, loopDevice
}
