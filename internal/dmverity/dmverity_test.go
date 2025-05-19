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
	"bytes"
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

			opts := DmverityOptions{
				Salt:          "0000000000000000000000000000000000000000000000000000000000000000",
				HashAlgorithm: "sha256",
				DataBlockSize: 4096,
				HashBlockSize: 4096,
				DataBlocks:    256,
				HashOffset:    1048576,
				HashType:      1,
				NoSuperblock:  false,                                  // Use superblock
				UUID:          "12345678-1234-1234-1234-123456789012", // Required for superblock
			}

			// Format with superblock - data and hash on same device
			rootHash, err := Format(loopDevice, loopDevice, &opts)
			assert.NoError(t, err)
			assert.NotEmpty(t, rootHash)

			// Open with superblock mode - provide hashOffset, opts is nil
			deviceName := testDeviceName + "-sb-same"
			devicePath, err := Open(loopDevice, deviceName, loopDevice, rootHash, opts.HashOffset, nil)
			assert.NoError(t, err)
			assert.Equal(t, "/dev/mapper/"+deviceName, devicePath)

			// Wait for device to appear (device-mapper symlink creation can be async)
			var statErr error
			for i := 0; i < 100; i++ {
				_, statErr = os.Stat(devicePath)
				if statErr == nil {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			assert.NoError(t, statErr)

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

			opts := DmverityOptions{
				Salt:          "0000000000000000000000000000000000000000000000000000000000000000",
				HashAlgorithm: "sha256",
				DataBlockSize: 4096,
				HashBlockSize: 4096,
				DataBlocks:    256,
				// HashOffset is REQUIRED even for separate devices when using superblock.
				// The library does not auto-calculate this - it must be explicitly provided.
				// This offset tells where the hash tree data begins after the superblock metadata.
				// Typically 4096 bytes (one block) is sufficient for superblock metadata.
				HashOffset:   4096,
				HashType:     1,
				NoSuperblock: false,                                  // Use superblock
				UUID:         "12345678-1234-5678-9012-123456789012", // Required for superblock
			}

			// Format with superblock - data and hash on separate devices
			rootHash, err := Format(dataDevice, hashDevice, &opts)
			assert.NoError(t, err)
			assert.NotEmpty(t, rootHash)

			// Open with superblock mode - separate devices
			deviceName := testDeviceName + "-sb-sep"
			devicePath, err := Open(dataDevice, deviceName, hashDevice, rootHash, opts.HashOffset, nil)
			assert.NoError(t, err)
			assert.Equal(t, "/dev/mapper/"+deviceName, devicePath)

			// Wait for device to appear (device-mapper symlink creation can be async)
			var statErr error
			for i := 0; i < 100; i++ {
				_, statErr = os.Stat(devicePath)
				if statErr == nil {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			assert.NoError(t, statErr)

			// Close device
			err = Close(deviceName)
			assert.NoError(t, err)

			// Verify device is removed
			_, err = os.Stat(devicePath)
			assert.True(t, os.IsNotExist(err))
		})

		t.Run("WithRootHashFile", func(t *testing.T) {
			tempDir := t.TempDir()
			_, loopDevice := createLoopbackDevice(t, tempDir, "1Mb")
			defer func() {
				assert.NoError(t, mount.DetachLoopDevice(loopDevice))
			}()

			// Create root hash file for format command
			rootHashFile, err := os.CreateTemp(tempDir, "root-hash-*.txt")
			assert.NoError(t, err)
			rootHashFilePath := rootHashFile.Name()
			rootHashFile.Close()
			defer os.Remove(rootHashFilePath)

			opts := DmverityOptions{
				Salt:          "0000000000000000000000000000000000000000000000000000000000000000",
				HashAlgorithm: "sha256",
				DataBlockSize: 4096,
				HashBlockSize: 4096,
				DataBlocks:    256,
				HashOffset:    1048576,
				HashType:      1,
				NoSuperblock:  false,
				UUID:          "12345678-1234-1234-1234-123456789012",
				RootHashFile:  rootHashFilePath,
			}

			// Format with root hash file
			rootHashFromFormat, err := Format(loopDevice, loopDevice, &opts)
			assert.NoError(t, err)
			assert.NotEmpty(t, rootHashFromFormat)

			// Verify root hash was written to file
			fileContent, err := os.ReadFile(rootHashFilePath)
			assert.NoError(t, err)
			fileHash := string(bytes.TrimSpace(fileContent))
			assert.Equal(t, rootHashFromFormat, fileHash)

			// Open using root hash from file
			deviceName := testDeviceName + "-roothashfile"
			optsOpen := opts
			optsOpen.RootHashFile = rootHashFilePath
			devicePath, err := Open(loopDevice, deviceName, loopDevice, "", opts.HashOffset, &optsOpen)
			assert.NoError(t, err)
			assert.Equal(t, "/dev/mapper/"+deviceName, devicePath)

			// Wait for device to appear
			var statErr error
			for i := 0; i < 100; i++ {
				_, statErr = os.Stat(devicePath)
				if statErr == nil {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			assert.NoError(t, statErr)

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

			opts := DmverityOptions{
				Salt:          "0000000000000000000000000000000000000000000000000000000000000000",
				HashAlgorithm: "sha256",
				DataBlockSize: 4096,
				HashBlockSize: 4096,
				DataBlocks:    256,
				HashOffset:    1048576,
				HashType:      1,
				NoSuperblock:  true, // No superblock
			}

			// Format without superblock - data and hash on same device
			rootHash, err := Format(loopDevice, loopDevice, &opts)
			assert.NoError(t, err)
			assert.NotEmpty(t, rootHash)

			// Open with no-superblock mode - provide opts with NoSuperblock=true
			deviceName := testDeviceName + "-nosb-same"
			devicePath, err := Open(loopDevice, deviceName, loopDevice, rootHash, opts.HashOffset, &opts)
			assert.NoError(t, err)
			assert.Equal(t, "/dev/mapper/"+deviceName, devicePath)

			// Wait for device to appear (device-mapper symlink creation can be async)
			var statErr error
			for i := 0; i < 100; i++ {
				_, statErr = os.Stat(devicePath)
				if statErr == nil {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			assert.NoError(t, statErr)

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

			opts := DmverityOptions{
				Salt:          "0000000000000000000000000000000000000000000000000000000000000000",
				HashAlgorithm: "sha256",
				DataBlockSize: 4096,
				HashBlockSize: 4096,
				DataBlocks:    256,
				HashOffset:    0, // Hash device is separate, starts at offset 0
				HashType:      1,
				NoSuperblock:  true, // No superblock
			}

			// Format without superblock - data and hash on separate devices
			rootHash, err := Format(dataDevice, hashDevice, &opts)
			assert.NoError(t, err)
			assert.NotEmpty(t, rootHash)

			// Open with no-superblock mode - separate devices
			deviceName := testDeviceName + "-nosb-sep"
			devicePath, err := Open(dataDevice, deviceName, hashDevice, rootHash, 0, &opts)
			assert.NoError(t, err)
			assert.Equal(t, "/dev/mapper/"+deviceName, devicePath)

			// Wait for device to appear (device-mapper symlink creation can be async)
			var statErr error
			for i := 0; i < 100; i++ {
				_, statErr = os.Stat(devicePath)
				if statErr == nil {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			assert.NoError(t, statErr)

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

func TestValidateOptions(t *testing.T) {
	tests := []struct {
		name    string
		opts    *DmverityOptions
		wantErr bool
		errMsg  string
	}{
		{
			name:    "nil options",
			opts:    nil,
			wantErr: true,
			errMsg:  "options cannot be nil",
		},
		{
			name: "valid options",
			opts: &DmverityOptions{
				Salt:          "0000000000000000000000000000000000000000000000000000000000000000",
				HashAlgorithm: "sha256",
				DataBlockSize: 4096,
				HashBlockSize: 4096,
			},
			wantErr: false,
		},
		{
			name: "invalid data block size",
			opts: &DmverityOptions{
				DataBlockSize: 1000, // not power of 2
			},
			wantErr: true,
			errMsg:  "data block size 1000 must be a power of 2",
		},
		{
			name: "invalid hash block size",
			opts: &DmverityOptions{
				HashBlockSize: 3000, // not power of 2
			},
			wantErr: true,
			errMsg:  "hash block size 3000 must be a power of 2",
		},
		{
			name: "invalid salt hex",
			opts: &DmverityOptions{
				Salt: "not-hex",
			},
			wantErr: true,
			errMsg:  "salt must be a valid hex string",
		},
		{
			name: "empty salt allowed",
			opts: &DmverityOptions{
				Salt: "",
			},
			wantErr: false,
		},
		{
			name: "valid power of 2 sizes",
			opts: &DmverityOptions{
				DataBlockSize: 512,
				HashBlockSize: 8192,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateOptions(tt.opts)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateRootHash(t *testing.T) {
	tests := []struct {
		name     string
		rootHash string
		wantErr  bool
		errMsg   string
	}{
		{
			name:     "empty root hash",
			rootHash: "",
			wantErr:  true,
			errMsg:   "root hash cannot be empty",
		},
		{
			name:     "valid root hash",
			rootHash: "abc123def456789012345678901234567890123456789012345678901234567890",
			wantErr:  false,
		},
		{
			name:     "invalid hex characters",
			rootHash: "xyz123",
			wantErr:  true,
			errMsg:   "root hash must be a valid hex string",
		},
		{
			name:     "odd length hex",
			rootHash: "abc12",
			wantErr:  true,
			errMsg:   "root hash must be a valid hex string",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRootHash(tt.rootHash)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestExtractRootHash(t *testing.T) {
	tests := []struct {
		name     string
		output   string
		opts     *DmverityOptions
		expected string
		wantErr  bool
		errMsg   string
	}{
		{
			name:    "empty output",
			output:  "",
			wantErr: true,
			errMsg:  "output is empty",
		},
		{
			name: "valid output",
			output: `VERITY header information for /dev/loop0
UUID:            	12345678-1234-1234-1234-123456789012
Hash type:       	1
Data blocks:     	256
Data block size: 	4096
Hash block size: 	4096
Hash algorithm:  	sha256
Salt:            	0000000000000000000000000000000000000000000000000000000000000000
Root hash:      	abc123def456789012345678901234567890123456789012345678901234567890`,
			expected: "abc123def456789012345678901234567890123456789012345678901234567890",
			wantErr:  false,
		},
		{
			name:    "missing root hash",
			output:  "Some output without Root hash line",
			wantErr: true,
			errMsg:  "root hash cannot be empty",
		},
		{
			name:    "invalid root hash",
			output:  `Root hash:      xyz123`,
			wantErr: true,
			errMsg:  "root hash must be a valid hex string",
		},
		{
			name: "root hash from nonexistent file",
			opts: &DmverityOptions{
				RootHashFile: "/tmp/nonexistent-roothash-file.txt",
			},
			wantErr: true,
			errMsg:  "failed to read root hash from file",
		},
		{
			name: "skips header lines",
			output: `Veritysetup 2.6.1 processing "format" action.
VERITY header information for /dev/loop0
Root hash:      	def456abc7890123456789012345678901234567890123456789012345678901`,
			expected: "def456abc7890123456789012345678901234567890123456789012345678901",
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ExtractRootHash(tt.output, tt.opts)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}

	// Separate test for root hash from file takes priority
	t.Run("root hash from file takes priority", func(t *testing.T) {
		tmpFile, err := os.CreateTemp("", "roothash-*.txt")
		assert.NoError(t, err)
		defer os.Remove(tmpFile.Name())

		expectedHash := "fedcba098765432109876543210987654321098765432109876543210987654321"
		_, err = tmpFile.WriteString(expectedHash + "\n")
		assert.NoError(t, err)
		tmpFile.Close()

		output := `Root hash:      different123456789012345678901234567890123456789012345678901234`
		opts := &DmverityOptions{
			RootHashFile: tmpFile.Name(),
		}

		result, err := ExtractRootHash(output, opts)
		assert.NoError(t, err)
		assert.Equal(t, expectedHash, result, "should use hash from file, not from output")
	})
}
