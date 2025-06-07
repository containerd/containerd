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

package dmverity

import (
	"os"
	"testing"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/pkg/testutil"
	"github.com/docker/go-units"
	"github.com/stretchr/testify/assert"
)

const (
	testDeviceName = "test-verity-device"
)

func TestDMVerity(t *testing.T) {
	testutil.RequiresRoot(t)

	// Skip if dm-verity is not supported
	supported, err := IsSupported()
	if !supported || err != nil {
		t.Skipf("dm-verity is not supported on this system: %v", err)
	}

	// Create temp directory for test files
	tempDir := t.TempDir()

	// Create a single loop device for both data and hash
	loopImage, loopDevice := createLoopbackDevice(t, tempDir, "1Mb")

	defer func() {
		err := mount.DetachLoopDevice(loopDevice)
		assert.NoError(t, err, "failed to detach loop device for image: %s", loopImage)
	}()

	// Create default options for the tests
	opts := DmverityOptions{
		HashAlgorithm: "sha256",
		Salt:          "1234000000000000000000000000000000000000000000000000000000000000",
		DataBlockSize: 4096,
		HashBlockSize: 4096,
		HashType:      1,
		UseSuperblock: false,
		Debug:         false,
		DataBlocks:    256,
		HashOffset:    1048576,
	}

	t.Run("IsSupported", func(t *testing.T) {
		supported, err := IsSupported()
		assert.True(t, supported, "dm-verity should be supported")
		assert.NoError(t, err, "IsSupported should not return an error")
	})

	var formatInfo *FormatOutputInfo

	t.Run("Format", func(t *testing.T) {
		var err error
		// Use the same device for both data and hash
		formatInfo, err = Format(loopDevice, loopDevice, &opts)
		assert.NoError(t, err, "failed to format dm-verity device")
		assert.NotEmpty(t, formatInfo.RootHash, "root hash should not be empty")
		t.Logf("Root hash: %s", formatInfo.RootHash)
	})

	t.Run("Open", func(t *testing.T) {
		output, err := Open(loopDevice, testDeviceName, loopDevice, formatInfo.RootHash, &opts)
		assert.NoError(t, err, "failed to open dm-verity device")
		t.Logf("Open output: %s", output)

		_, err = os.Stat("/dev/mapper/" + testDeviceName)
		assert.NoError(t, err, "device should exist in /dev/mapper")
	})

	t.Run("Close", func(t *testing.T) {
		output, err := Close(testDeviceName)
		assert.NoError(t, err, "failed to close dm-verity device")
		t.Logf("Close output: %s", output)

		_, err = os.Stat("/dev/mapper/" + testDeviceName)
		assert.True(t, os.IsNotExist(err), "device should not exist after closing")
	})
}

func createLoopbackDevice(t *testing.T, dir string, size string) (string, string) {
	file, err := os.CreateTemp(dir, "dmverity-tests-")
	assert.NoError(t, err)

	sizeInBytes, err := units.RAMInBytes(size)
	assert.NoError(t, err)

	err = file.Truncate(sizeInBytes * 2)
	assert.NoError(t, err)

	err = file.Close()
	assert.NoError(t, err)

	imagePath := file.Name()

	loopDevice, err := mount.AttachLoopDevice(imagePath)
	assert.NoError(t, err)

	return imagePath, loopDevice
}
