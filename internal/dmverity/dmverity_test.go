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
		UseSuperblock: true,
		Debug:         false,
	}

	t.Run("IsSupported", func(t *testing.T) {
		supported, err := IsSupported()
		assert.True(t, supported)
		assert.NoError(t, err)
	})

	var rootHash string

	t.Run("Format", func(t *testing.T) {
		var err error
		rootHash, err = Format(loopDevice, loopDevice, &opts)
		assert.NoError(t, err)
		assert.NotEmpty(t, rootHash)
	})

	t.Run("Format_WithRootHashFile", func(t *testing.T) {
		rootHashFile, err := os.CreateTemp(tempDir, "root-hash-*.txt")
		assert.NoError(t, err)
		rootHashFilePath := rootHashFile.Name()
		rootHashFile.Close()
		defer os.Remove(rootHashFilePath)

		_, loopDevice2 := createLoopbackDevice(t, tempDir, "1Mb")
		defer func() {
			assert.NoError(t, mount.DetachLoopDevice(loopDevice2))
		}()

		optsWithFile := opts
		optsWithFile.RootHashFile = rootHashFilePath

		rootHashFromFormat, err := Format(loopDevice2, loopDevice2, &optsWithFile)
		assert.NoError(t, err)
		assert.NotEmpty(t, rootHashFromFormat)

		fileContent, err := os.ReadFile(rootHashFilePath)
		assert.NoError(t, err)
		fileHash := string(bytes.TrimSpace(fileContent))
		assert.Equal(t, rootHashFromFormat, fileHash)
	})

	t.Run("Format_NoSuperblock", func(t *testing.T) {
		_, loopDevice3 := createLoopbackDevice(t, tempDir, "1Mb")
		defer func() {
			assert.NoError(t, mount.DetachLoopDevice(loopDevice3))
		}()

		optsNoSuperblock := opts
		optsNoSuperblock.UseSuperblock = false

		rootHashNoSuperblock, err := Format(loopDevice3, loopDevice3, &optsNoSuperblock)
		assert.NoError(t, err)
		assert.NotEmpty(t, rootHashNoSuperblock)
	})

	t.Run("Open", func(t *testing.T) {
		_, err := Open(loopDevice, testDeviceName, loopDevice, rootHash, &opts)
		assert.NoError(t, err)

		_, err = os.Stat("/dev/mapper/" + testDeviceName)
		assert.NoError(t, err)
	})

	t.Run("Open_WithRootHashFile", func(t *testing.T) {
		// Create a root hash file
		rootHashFile, err := os.CreateTemp(tempDir, "root-hash-open-*.txt")
		assert.NoError(t, err)
		rootHashFilePath := rootHashFile.Name()

		// Write the root hash to the file
		_, err = rootHashFile.WriteString(rootHash)
		assert.NoError(t, err)
		rootHashFile.Close()
		defer os.Remove(rootHashFilePath)

		// Create a new loopback device for this test
		_, loopDevice4 := createLoopbackDevice(t, tempDir, "1Mb")
		defer func() {
			assert.NoError(t, mount.DetachLoopDevice(loopDevice4))
		}()

		// Format the device first
		optsFormat := opts
		_, err = Format(loopDevice4, loopDevice4, &optsFormat)
		assert.NoError(t, err)

		// Open with root hash file instead of command-line arg
		optsOpen := opts
		optsOpen.RootHashFile = rootHashFilePath
		deviceName := "test-verity-roothashfile"
		_, err = Open(loopDevice4, deviceName, loopDevice4, "", &optsOpen)
		assert.NoError(t, err)

		// Verify device was created
		_, err = os.Stat("/dev/mapper/" + deviceName)
		assert.NoError(t, err)

		// Clean up
		_, err = Close(deviceName)
		assert.NoError(t, err)
	})

	t.Run("Close", func(t *testing.T) {
		_, err := Close(testDeviceName)
		assert.NoError(t, err)

		_, err = os.Stat("/dev/mapper/" + testDeviceName)
		assert.True(t, os.IsNotExist(err))
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
			name:    "valid options",
			opts:    func() *DmverityOptions { o := DefaultDmverityOptions(); return &o }(),
			wantErr: false,
		},
		{
			name: "invalid data block size",
			opts: func() *DmverityOptions {
				o := DefaultDmverityOptions()
				o.DataBlockSize = 4097 // Not a power of 2
				return &o
			}(),
			wantErr: true,
			errMsg:  "data block size",
		},
		{
			name: "invalid hash block size",
			opts: func() *DmverityOptions {
				o := DefaultDmverityOptions()
				o.HashBlockSize = 4097 // Not a power of 2
				return &o
			}(),
			wantErr: true,
			errMsg:  "hash block size",
		},
		{
			name: "invalid salt hex",
			opts: func() *DmverityOptions {
				o := DefaultDmverityOptions()
				o.Salt = "not-a-hex-string"
				return &o
			}(),
			wantErr: true,
			errMsg:  "salt must be a valid hex string",
		},
		{
			name: "empty salt allowed",
			opts: func() *DmverityOptions {
				o := DefaultDmverityOptions()
				o.Salt = ""
				return &o
			}(),
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

	// Test multiple valid power-of-2 sizes
	t.Run("valid power of 2 sizes", func(t *testing.T) {
		validSizes := []uint32{512, 1024, 2048, 4096, 8192}
		for _, size := range validSizes {
			opts := DefaultDmverityOptions()
			opts.DataBlockSize = size
			opts.HashBlockSize = size
			err := ValidateOptions(&opts)
			assert.NoError(t, err, "size %d should be valid", size)
		}
	})
}

func TestValidateRootHash(t *testing.T) {
	tests := []struct {
		name    string
		hash    string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "empty root hash",
			hash:    "",
			wantErr: true,
			errMsg:  "root hash cannot be empty",
		},
		{
			name:    "valid root hash",
			hash:    "bef46122f85025cf37061b16c04e2a19960a5bbcdbb656b5e91ae7927c0ad807",
			wantErr: false,
		},
		{
			name:    "invalid hex characters",
			hash:    "not-a-valid-hex-hash",
			wantErr: true,
			errMsg:  "root hash must be a valid hex string",
		},
		{
			name:    "odd length hex",
			hash:    "abc",
			wantErr: true,
			errMsg:  "root hash must be a valid hex string",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRootHash(tt.hash)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestExtractRootHash(t *testing.T) {
	validOutput := `VERITY header information for /dev/loop0
UUID:                   eebbec4a-a914-4089-aca0-22266b21bd2b
Hash type:              1
Data blocks:            256
Data block size:        4096
Hash block size:        4096
Hash algorithm:         sha256
Salt:                   0000000000000000000000000000000000000000000000000000000000000000
Root hash:              bef46122f85025cf37061b16c04e2a19960a5bbcdbb656b5e91ae7927c0ad807`

	tests := []struct {
		name    string
		output  string
		opts    *DmverityOptions
		wantErr bool
		errMsg  string
		check   func(*testing.T, string)
	}{
		{
			name:    "empty output",
			output:  "",
			wantErr: true,
			errMsg:  "output is empty",
		},
		{
			name:    "valid output",
			output:  validOutput,
			wantErr: false,
			check: func(t *testing.T, rootHash string) {
				assert.Equal(t, "bef46122f85025cf37061b16c04e2a19960a5bbcdbb656b5e91ae7927c0ad807", rootHash)
			},
		},
		{
			name: "missing root hash",
			output: `VERITY header information for /dev/loop0
UUID:                   eebbec4a-a914-4089-aca0-22266b21bd2b
Hash type:              1
Data blocks:            256
Data block size:        4096
Hash block size:        4096
Hash algorithm:         sha256
Salt:                   0000000000000000000000000000000000000000000000000000000000000000`,
			wantErr: true,
			errMsg:  "root hash",
		},
		{
			name: "invalid root hash",
			output: `VERITY header information for /dev/loop0
UUID:                   eebbec4a-a914-4089-aca0-22266b21bd2b
Hash type:              1
Data blocks:            256
Data block size:        4096
Hash block size:        4096
Hash algorithm:         sha256
Salt:                   0000000000000000000000000000000000000000000000000000000000000000
Root hash:              not-a-valid-hex-hash`,
			wantErr: true,
			errMsg:  "root hash is invalid",
		},
		{
			name: "root hash from nonexistent file",
			output: `VERITY header information for /dev/loop0
UUID:                   eebbec4a-a914-4089-aca0-22266b21bd2b
Hash type:              1
Data blocks:            256
Data block size:        4096
Hash block size:        4096
Hash algorithm:         sha256
Salt:                   0000000000000000000000000000000000000000000000000000000000000000`,
			opts:    &DmverityOptions{RootHashFile: "/nonexistent/path/to/hash/file"},
			wantErr: true,
			errMsg:  "failed to read root hash from file",
		},
		{
			name: "root hash from file takes priority",
			output: `VERITY header information for /dev/loop0
UUID:                   eebbec4a-a914-4089-aca0-22266b21bd2b
Hash type:              1
Data blocks:            256
Data block size:        4096
Hash block size:        4096
Hash algorithm:         sha256
Salt:                   0000000000000000000000000000000000000000000000000000000000000000
Root hash:              aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa`,
			opts: func() *DmverityOptions {
				// Create a temporary file with a different hash
				tmpFile, err := os.CreateTemp("", "root-hash-test-*.txt")
				assert.NoError(t, err)
				tmpFilePath := tmpFile.Name()
				fileHash := "bef46122f85025cf37061b16c04e2a19960a5bbcdbb656b5e91ae7927c0ad807"
				_, err = tmpFile.WriteString(fileHash)
				assert.NoError(t, err)
				tmpFile.Close()
				// Cleanup will happen after the test runs
				t.Cleanup(func() { os.Remove(tmpFilePath) })
				return &DmverityOptions{RootHashFile: tmpFilePath}
			}(),
			wantErr: false,
			check: func(t *testing.T, rootHash string) {
				// Should get the hash from file, not from output
				assert.Equal(t, "bef46122f85025cf37061b16c04e2a19960a5bbcdbb656b5e91ae7927c0ad807", rootHash)
			},
		},
		{
			name: "skips header lines",
			output: `VERITY header information for /dev/loop0
# veritysetup format /dev/loop0 /dev/loop0
UUID:                   eebbec4a-a914-4089-aca0-22266b21bd2b
Hash type:              1
Data blocks:            256
Data block size:        4096
Hash block size:        4096
Hash algorithm:         sha256
Salt:                   0000000000000000000000000000000000000000000000000000000000000000
Root hash:              bef46122f85025cf37061b16c04e2a19960a5bbcdbb656b5e91ae7927c0ad807`,
			wantErr: false,
			check: func(t *testing.T, rootHash string) {
				assert.Equal(t, "bef46122f85025cf37061b16c04e2a19960a5bbcdbb656b5e91ae7927c0ad807", rootHash)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rootHash, err := ExtractRootHash(tt.output, tt.opts)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
				assert.NotEmpty(t, rootHash)
				if tt.check != nil {
					tt.check(t, rootHash)
				}
			}
		})
	}
}

func TestMetadataPath(t *testing.T) {
	assert.Equal(t, "/path/to/layer.erofs.dmverity", MetadataPath("/path/to/layer.erofs"))
}

func TestParseMetadata(t *testing.T) {
	tmpDir := t.TempDir()

	createMetadata := func(filename, content string) string {
		layerBlob := tmpDir + "/" + strings.TrimSuffix(filename, ".dmverity")
		os.WriteFile(tmpDir+"/"+filename, []byte(content), 0644)
		return layerBlob
	}

	// Valid cases
	layerBlob := createMetadata("layer.erofs.dmverity",
		"roothash=abc123def456\nhash-offset=8192\nuse-superblock=true\n")
	m, err := ParseMetadata(layerBlob)
	assert.NoError(t, err)
	assert.Equal(t, "abc123def456", m.RootHash)
	assert.Equal(t, uint64(8192), m.HashOffset)
	assert.True(t, m.UseSuperblock)

	// use-superblock=false
	layerBlob = createMetadata("layer2.erofs.dmverity", "roothash=def456\nhash-offset=16384\nuse-superblock=false\n")
	m, _ = ParseMetadata(layerBlob)
	assert.False(t, m.UseSuperblock)

	// Error cases
	layerBlob = createMetadata("layer3.erofs.dmverity", "hash-offset=8192\n")
	_, err = ParseMetadata(layerBlob)
	assert.ErrorContains(t, err, "roothash not found")

	layerBlob = createMetadata("layer4.erofs.dmverity", "roothash=abc\n")
	_, err = ParseMetadata(layerBlob)
	assert.ErrorContains(t, err, "hash-offset not found")

	_, err = ParseMetadata(tmpDir + "/nonexistent.erofs")
	assert.ErrorContains(t, err, "metadata file not found")
}
