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
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/pkg/testutil"
	"github.com/containerd/containerd/snapshots/devmapper/dmsetup"
	"github.com/containerd/log"
	"github.com/docker/go-units"
	"github.com/stretchr/testify/assert"
	exec "golang.org/x/sys/execabs"
)

const (
	thinDevice1 = "thin-1"
	thinDevice2 = "thin-2"
	snapDevice1 = "snap-1"
	device1Size = 1000000
	device2Size = 2000000
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

	assert.NoError(t, log.SetLevel("debug"))
	ctx := context.Background()

	tempDir := t.TempDir()

	_, loopDataDevice := createLoopbackDevice(t, tempDir)
	_, loopMetaDevice := createLoopbackDevice(t, tempDir)

	poolName := fmt.Sprintf("test-pool-device-%d", time.Now().Nanosecond())
	err := dmsetup.CreatePool(poolName, loopDataDevice, loopMetaDevice, 64*1024/dmsetup.SectorSize)
	assert.Nil(t, err, "failed to create pool %q", poolName)

	defer func() {
		// Detach loop devices and remove images
		err := mount.DetachLoopDevice(loopDataDevice, loopMetaDevice)
		assert.NoError(t, err)
	}()

	config := &Config{
		PoolName:           poolName,
		RootPath:           tempDir,
		BaseImageSize:      "16mb",
		BaseImageSizeBytes: 16 * 1024 * 1024,
		DiscardBlocks:      true,
	}

	pool, err := NewPoolDevice(ctx, config)
	assert.Nil(t, err, "can't create device pool")
	assert.True(t, pool != nil)

	defer func() {
		err := pool.RemovePool(ctx)
		assert.Nil(t, err, "can't close device pool")
	}()

	// Create thin devices
	t.Run("CreateThinDevice", func(t *testing.T) {
		testCreateThinDevice(t, pool)
	})

	// Make ext4 filesystem on 'thin-1'
	t.Run("MakeFileSystem", func(t *testing.T) {
		testMakeFileSystem(t, pool)
	})

	// Mount 'thin-1' and write v1 test file on 'thin-1' device
	err = mount.WithTempMount(ctx, getMounts(thinDevice1), func(thin1MountPath string) error {
		// Write v1 test file on 'thin-1' device
		thin1TestFilePath := filepath.Join(thin1MountPath, "TEST")
		err := os.WriteFile(thin1TestFilePath, []byte("test file (v1)"), 0700)
		assert.Nil(t, err, "failed to write test file v1 on '%s' volume", thinDevice1)

		return nil
	})

	// Take snapshot of 'thin-1'
	t.Run("CreateSnapshotDevice", func(t *testing.T) {
		testCreateSnapshot(t, pool)
	})

	// Update TEST file on 'thin-1' to v2
	err = mount.WithTempMount(ctx, getMounts(thinDevice1), func(thin1MountPath string) error {
		thin1TestFilePath := filepath.Join(thin1MountPath, "TEST")
		err = os.WriteFile(thin1TestFilePath, []byte("test file (v2)"), 0700)
		assert.Nil(t, err, "failed to write test file v2 on 'thin-1' volume after taking snapshot")

		return nil
	})

	assert.NoError(t, err)

	// Mount 'snap-1' and make sure TEST file is v1
	err = mount.WithTempMount(ctx, getMounts(snapDevice1), func(snap1MountPath string) error {
		// Read test file from snapshot device and make sure it's v1
		fileData, err := os.ReadFile(filepath.Join(snap1MountPath, "TEST"))
		assert.Nil(t, err, "couldn't read test file from '%s' device", snapDevice1)
		assert.Equal(t, "test file (v1)", string(fileData), "test file content is invalid on snapshot")

		return nil
	})

	assert.NoError(t, err)

	t.Run("DeactivateDevice", func(t *testing.T) {
		testDeactivateThinDevice(t, pool)
	})

	t.Run("RemoveDevice", func(t *testing.T) {
		testRemoveThinDevice(t, pool)
	})

	t.Run("rollbackActivate", func(t *testing.T) {
		testCreateThinDevice(t, pool)

		ctx := context.Background()

		snapDevice := "snap2"

		err := pool.CreateSnapshotDevice(ctx, thinDevice1, snapDevice, device1Size)
		assert.NoError(t, err)

		info, err := pool.metadata.GetDevice(ctx, snapDevice)
		assert.NoError(t, err)

		// Simulate a case that the device cannot be activated.
		err = pool.DeactivateDevice(ctx, info.Name, false, false)
		assert.NoError(t, err)

		err = pool.rollbackActivate(ctx, info, err)
		assert.NoError(t, err)
	})
}

func TestPoolDeviceMarkFaulty(t *testing.T) {
	store := createStore(t)
	defer cleanupStore(t, store)

	err := store.AddDevice(testCtx, &DeviceInfo{Name: "1", State: Unknown})
	assert.NoError(t, err)

	// Note: do not use 'Activated' here because pool.ensureDeviceStates() will
	// try to activate the real dm device, which will fail on a faked device.
	err = store.AddDevice(testCtx, &DeviceInfo{Name: "2", State: Deactivated})
	assert.NoError(t, err)

	pool := &PoolDevice{metadata: store}
	err = pool.ensureDeviceStates(testCtx)
	assert.NoError(t, err)

	called := 0
	err = pool.metadata.WalkDevices(testCtx, func(info *DeviceInfo) error {
		called++

		switch called {
		case 1:
			assert.Equal(t, Faulty, info.State)
			assert.Equal(t, "1", info.Name)
		case 2:
			assert.Equal(t, Deactivated, info.State)
			assert.Equal(t, "2", info.Name)
		default:
			t.Error("unexpected walk call")
		}

		return nil
	})
	assert.NoError(t, err)
	assert.Equal(t, 2, called)
}

func testCreateThinDevice(t *testing.T, pool *PoolDevice) {
	ctx := context.Background()

	err := pool.CreateThinDevice(ctx, thinDevice1, device1Size)
	assert.Nil(t, err, "can't create first thin device")

	err = pool.CreateThinDevice(ctx, thinDevice1, device1Size)
	assert.True(t, err != nil, "device pool allows duplicated device names")

	err = pool.CreateThinDevice(ctx, thinDevice2, device2Size)
	assert.Nil(t, err, "can't create second thin device")

	deviceInfo1, err := pool.metadata.GetDevice(ctx, thinDevice1)
	assert.NoError(t, err)

	deviceInfo2, err := pool.metadata.GetDevice(ctx, thinDevice2)
	assert.NoError(t, err)

	assert.True(t, deviceInfo1.DeviceID != deviceInfo2.DeviceID, "assigned device ids should be different")

	usage, err := pool.GetUsage(thinDevice1)
	assert.NoError(t, err)
	assert.Equal(t, usage, int64(0))
}

func testMakeFileSystem(t *testing.T, pool *PoolDevice) {
	devicePath := dmsetup.GetFullDevicePath(thinDevice1)
	args := []string{
		devicePath,
		"-E",
		"nodiscard,lazy_itable_init=0,lazy_journal_init=0",
	}

	output, err := exec.Command("mkfs.ext4", args...).CombinedOutput()
	assert.Nil(t, err, "failed to make filesystem on '%s': %s", thinDevice1, string(output))

	usage, err := pool.GetUsage(thinDevice1)
	assert.NoError(t, err)
	assert.True(t, usage > 0)
}

func testCreateSnapshot(t *testing.T, pool *PoolDevice) {
	err := pool.CreateSnapshotDevice(context.Background(), thinDevice1, snapDevice1, device1Size)
	assert.Nil(t, err, "failed to create snapshot from '%s' volume", thinDevice1)
}

func testDeactivateThinDevice(t *testing.T, pool *PoolDevice) {
	deviceList := []string{
		thinDevice2,
		snapDevice1,
	}

	for _, deviceName := range deviceList {
		assert.True(t, pool.IsActivated(deviceName))

		err := pool.DeactivateDevice(context.Background(), deviceName, false, true)
		assert.Nil(t, err, "failed to remove '%s'", deviceName)

		assert.False(t, pool.IsActivated(deviceName))
	}
}

func testRemoveThinDevice(t *testing.T, pool *PoolDevice) {
	err := pool.RemoveDevice(testCtx, thinDevice1)
	assert.Nil(t, err, "should delete thin device from pool")

	err = pool.RemoveDevice(testCtx, thinDevice2)
	assert.Nil(t, err, "should delete thin device from pool")
}

func getMounts(thinDeviceName string) []mount.Mount {
	return []mount.Mount{
		{
			Source: dmsetup.GetFullDevicePath(thinDeviceName),
			Type:   "ext4",
		},
	}
}

func createLoopbackDevice(t *testing.T, dir string) (string, string) {
	file, err := os.CreateTemp(dir, testsPrefix)
	assert.NoError(t, err)

	size, err := units.RAMInBytes("128Mb")
	assert.NoError(t, err)

	err = file.Truncate(size)
	assert.NoError(t, err)

	err = file.Close()
	assert.NoError(t, err)

	imagePath := file.Name()

	loopDevice, err := mount.AttachLoopDevice(imagePath)
	assert.NoError(t, err)

	return imagePath, loopDevice
}
