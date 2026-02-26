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
	"strings"
	"testing"
	"time"

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

	supported, err := IsSupported()
	if !supported || err != nil {
		t.Skipf("dm-verity is not supported on this system: %v", err)
	}

	t.Run("IsSupported", func(t *testing.T) {
		supported, err := IsSupported()
		assert.True(t, supported)
		assert.NoError(t, err)
	})

	t.Run("WithSuperblock", func(t *testing.T) {
		t.Run("SameDevice", func(t *testing.T) {
			tempDir := t.TempDir()
			_, loopDevice := createLoopbackDevice(t, tempDir, "1Mb")
			defer func() {
				assert.NoError(t, mount.DetachLoopDevice(loopDevice))
			}()

			opts := testOptions(true, 1048576)

			// Format with superblock - data and hash on same device
			rootHash, err := Format(loopDevice, loopDevice, &opts)
			assert.NoError(t, err)
			assert.NotEmpty(t, rootHash)

			// Open with superblock mode - provide hashOffset, opts is nil
			deviceName := testDeviceName + "-sb-same"
			devicePath, err := Open(loopDevice, deviceName, loopDevice, rootHash, opts.HashOffset, nil)
			assert.NoError(t, err)
			assert.Equal(t, "/dev/mapper/"+deviceName, devicePath)

			waitForDevice(t, devicePath)

			// Close device
			err = Close(deviceName)
			assert.NoError(t, err)

			// Verify device is removed
			_, err = os.Stat(devicePath)
			assert.True(t, os.IsNotExist(err))
		})

		t.Run("SeparateDevices", func(t *testing.T) {
			tempDir := t.TempDir()
			_, dataDevice := createLoopbackDevice(t, tempDir, "1Mb")
			_, hashDevice := createLoopbackDevice(t, tempDir, "512Kb")
			defer func() {
				assert.NoError(t, mount.DetachLoopDevice(dataDevice))
				assert.NoError(t, mount.DetachLoopDevice(hashDevice))
			}()

			// HashOffset is REQUIRED even for separate devices when using superblock.
			// Typically 4096 bytes (one block) is sufficient for superblock metadata.
			opts := testOptions(true, 4096)
			opts.UUID = "12345678-1234-5678-9012-123456789012" // Different UUID for variety

			// Format with superblock - data and hash on separate devices
			rootHash, err := Format(dataDevice, hashDevice, &opts)
			assert.NoError(t, err)
			assert.NotEmpty(t, rootHash)

			// Open with superblock mode - separate devices
			deviceName := testDeviceName + "-sb-sep"
			devicePath, err := Open(dataDevice, deviceName, hashDevice, rootHash, opts.HashOffset, nil)
			assert.NoError(t, err)
			assert.Equal(t, "/dev/mapper/"+deviceName, devicePath)

			waitForDevice(t, devicePath)

			// Close device
			err = Close(deviceName)
			assert.NoError(t, err)

			// Verify device is removed
			_, err = os.Stat(devicePath)
			assert.True(t, os.IsNotExist(err))
		})
	})

	t.Run("NoSuperblock", func(t *testing.T) {
		t.Run("SameDevice", func(t *testing.T) {
			tempDir := t.TempDir()
			_, loopDevice := createLoopbackDevice(t, tempDir, "1Mb")
			defer func() {
				assert.NoError(t, mount.DetachLoopDevice(loopDevice))
			}()

			opts := testOptions(false, 1048576)

			// Format without superblock - data and hash on same device
			rootHash, err := Format(loopDevice, loopDevice, &opts)
			assert.NoError(t, err)
			assert.NotEmpty(t, rootHash)

			// Open with no-superblock mode - provide opts with NoSuperblock=true
			deviceName := testDeviceName + "-nosb-same"
			devicePath, err := Open(loopDevice, deviceName, loopDevice, rootHash, 0, &opts)
			assert.NoError(t, err)
			assert.Equal(t, "/dev/mapper/"+deviceName, devicePath)

			waitForDevice(t, devicePath)

			// Close device
			err = Close(deviceName)
			assert.NoError(t, err)

			// Verify device is removed
			_, err = os.Stat(devicePath)
			assert.True(t, os.IsNotExist(err))
		})

		t.Run("SeparateDevices", func(t *testing.T) {
			tempDir := t.TempDir()
			_, dataDevice := createLoopbackDevice(t, tempDir, "1Mb")
			_, hashDevice := createLoopbackDevice(t, tempDir, "512Kb")
			defer func() {
				assert.NoError(t, mount.DetachLoopDevice(dataDevice))
				assert.NoError(t, mount.DetachLoopDevice(hashDevice))
			}()

			opts := testOptions(false, 0) // Hash device is separate, starts at offset 0

			// Format without superblock - data and hash on separate devices
			rootHash, err := Format(dataDevice, hashDevice, &opts)
			assert.NoError(t, err)
			assert.NotEmpty(t, rootHash)

			// Open with no-superblock mode - separate devices
			deviceName := testDeviceName + "-nosb-sep"
			devicePath, err := Open(dataDevice, deviceName, hashDevice, rootHash, 0, &opts)
			assert.NoError(t, err)
			assert.Equal(t, "/dev/mapper/"+deviceName, devicePath)

			waitForDevice(t, devicePath)

			// Close device
			err = Close(deviceName)
			assert.NoError(t, err)

			// Verify device is removed
			_, err = os.Stat(devicePath)
			assert.True(t, os.IsNotExist(err))
		})
	})
}

func createLoopbackDevice(t *testing.T, dir string, size string) (string, string) {
	t.Helper()
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

// waitForDevice waits for a device-mapper device to appear in /dev/mapper
func waitForDevice(t *testing.T, devicePath string) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if _, err := os.Stat(devicePath); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("device %s did not appear after waiting", devicePath)
}

// testOptions creates DmverityOptions for testing with common defaults
func testOptions(superblock bool, hashOffset uint64) DmverityOptions {
	opts := DmverityOptions{
		Salt:          "0000000000000000000000000000000000000000000000000000000000000000",
		HashAlgorithm: "sha256",
		DataBlockSize: 4096,
		HashBlockSize: 4096,
		DataBlocks:    256,
		HashOffset:    hashOffset,
		HashType:      1,
		NoSuperblock:  !superblock,
	}
	if superblock {
		opts.UUID = "12345678-1234-1234-1234-123456789012"
	}
	return opts
}

// createTempFile creates a temporary file with optional data, returns file path and cleanup function
func createTempFile(t *testing.T, data []byte) string {
	t.Helper()
	file, err := os.CreateTemp("", "test-data-*.img")
	assert.NoError(t, err)
	if len(data) > 0 {
		_, err = file.Write(data)
		assert.NoError(t, err)
	}
	err = file.Close()
	assert.NoError(t, err)
	t.Cleanup(func() { os.Remove(file.Name()) })
	return file.Name()
}

func TestMetadataPath(t *testing.T) {
	assert.Equal(t, "/path/to/layer.erofs.dmverity", MetadataPath("/path/to/layer.erofs"))
}

func TestDevicePath(t *testing.T) {
	assert.Equal(t, "/dev/mapper/test-device", DevicePath("test-device"))
	assert.Equal(t, "/dev/mapper/containerd-erofs-abc123", DevicePath("containerd-erofs-abc123"))
}

func TestReadMetadata(t *testing.T) {
	tmpDir := t.TempDir()

	createMetadataFile := func(filename, content string) string {
		layerBlob := tmpDir + "/" + strings.TrimSuffix(filename, ".dmverity")
		os.WriteFile(tmpDir+"/"+filename, []byte(content), 0644)
		return layerBlob
	}

	// Valid case
	layerBlob := createMetadataFile("layer.erofs.dmverity", `{"roothash":"abc123def456789012345678901234567890123456789012345678901234","hashoffset":12288}`)
	metadata, err := ReadMetadata(layerBlob)
	assert.NoError(t, err)
	assert.Equal(t, "abc123def456789012345678901234567890123456789012345678901234", metadata.RootHash)
	assert.Equal(t, uint64(12288), metadata.HashOffset)

	// Valid case with pretty-printed JSON
	layerBlob = createMetadataFile("layer2.erofs.dmverity", `{
  "roothash": "def456789012345678901234567890123456789012345678901234567890",
  "hashoffset": 16384
}`)
	metadata, err = ReadMetadata(layerBlob)
	assert.NoError(t, err)
	assert.Equal(t, "def456789012345678901234567890123456789012345678901234567890", metadata.RootHash)
	assert.Equal(t, uint64(16384), metadata.HashOffset)

	// Error: empty root hash
	layerBlob = createMetadataFile("layer3.erofs.dmverity", `{"roothash":"","hashoffset":12288}`)
	_, err = ReadMetadata(layerBlob)
	assert.ErrorContains(t, err, "missing root hash")

	// Error: missing root hash field
	layerBlob = createMetadataFile("layer4.erofs.dmverity", `{"hashoffset":12288}`)
	_, err = ReadMetadata(layerBlob)
	assert.ErrorContains(t, err, "missing root hash")

	// Error: invalid JSON
	layerBlob = createMetadataFile("layer5.erofs.dmverity", `not valid json`)
	_, err = ReadMetadata(layerBlob)
	assert.ErrorContains(t, err, "failed to parse")

	// Error: file not found
	_, err = ReadMetadata(tmpDir + "/nonexistent.erofs")
	assert.ErrorContains(t, err, "metadata file not found")
}

// TestErrorHandling tests error cases for dm-verity functions
func TestErrorHandling(t *testing.T) {
	testutil.RequiresRoot(t)

	isSupported, err := IsSupported()
	if err != nil || !isSupported {
		t.Skip("dm-verity not supported on this system")
	}

	t.Run("Open_EmptyRootHash", func(t *testing.T) {
		dataFile := createTempFile(t, nil)
		_, err := Open(dataFile, "test-device", dataFile, "", 0, nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "rootHash cannot be empty")
	})

	t.Run("Open_InvalidRootHash", func(t *testing.T) {
		dataFile := createTempFile(t, nil)
		_, err := Open(dataFile, "test-device", dataFile, "not-a-valid-hex-string", 0, nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid root hash")
	})

	t.Run("Open_NonexistentDevice", func(t *testing.T) {
		_, err = Open("/nonexistent/device.img", "test-device", "/nonexistent/device.img", "abc123", 0, nil)
		assert.Error(t, err)
	})

	t.Run("Open_InvalidSalt", func(t *testing.T) {
		dataFile := createTempFile(t, nil)
		opts := DefaultDmverityOptions()
		opts.NoSuperblock = true
		opts.Salt = "invalid-hex-string"
		_, err := Open(dataFile, "test-device", dataFile, "abc123def456", 0, opts)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid salt")
	})

	t.Run("Close_NonexistentDevice", func(t *testing.T) {
		err := Close("nonexistent-device-12345")
		assert.Error(t, err)
	})

	t.Run("VerifyDevice_EmptyRootHash", func(t *testing.T) {
		err := VerifyDevice("test-device", "")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid root hash")
	})

	t.Run("VerifyDevice_InvalidRootHash", func(t *testing.T) {
		err := VerifyDevice("test-device", "not-valid-hex")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid root hash")
	})

	t.Run("VerifyDevice_NonexistentDevice", func(t *testing.T) {
		err := VerifyDevice("nonexistent-device-12345", "abc123def456")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "verification failed")
	})

	t.Run("Format_InvalidSalt", func(t *testing.T) {
		dataFile := createTempFile(t, make([]byte, 1024*1024))
		opts := DefaultDmverityOptions()
		opts.Salt = "invalid-hex-string-not-valid"
		_, err := Format(dataFile, dataFile, opts)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid salt")
	})

	t.Run("Format_InvalidUUID", func(t *testing.T) {
		dataFile := createTempFile(t, make([]byte, 1024*1024))
		opts := DefaultDmverityOptions()
		opts.UUID = "not-a-valid-uuid-format"
		_, err := Format(dataFile, dataFile, opts)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid UUID")
	})
}
