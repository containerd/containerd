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
	"fmt"
	"os"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/go-dmverity/pkg/utils"
	"github.com/containerd/go-dmverity/pkg/verity"
)

func IsSupported() (bool, error) {
	moduleData, err := os.ReadFile("/proc/modules")
	if err != nil {
		return false, fmt.Errorf("failed to read /proc/modules: %w", err)
	}
	if !bytes.Contains(moduleData, []byte("dm_verity")) {
		return false, fmt.Errorf("dm_verity module not loaded")
	}

	return true, nil
}

func convertToVerityParams(opts *DmverityOptions) (verity.Params, error) {
	params := verity.DefaultParams()

	if opts != nil {
		if opts.HashAlgorithm != "" {
			params.HashName = opts.HashAlgorithm
		}
		if opts.DataBlockSize > 0 {
			params.DataBlockSize = opts.DataBlockSize
		}
		if opts.HashBlockSize > 0 {
			params.HashBlockSize = opts.HashBlockSize
		}
		if opts.DataBlocks > 0 {
			params.DataBlocks = opts.DataBlocks
		}
		if opts.HashOffset > 0 {
			params.HashAreaOffset = opts.HashOffset
		}
		if opts.HashType > 0 {
			params.HashType = opts.HashType
		}

		if opts.Salt != "" {
			salt, saltSize, err := utils.ApplySalt(opts.Salt, 256)
			if err != nil {
				return params, fmt.Errorf("invalid salt: %w", err)
			}
			params.Salt = salt
			params.SaltSize = saltSize
		}

		if opts.UUID != "" {
			uuidBytes, err := utils.ApplyUUID(opts.UUID, false, opts.NoSuperblock, nil)
			if err != nil {
				return params, fmt.Errorf("invalid UUID: %w", err)
			}
			params.UUID = uuidBytes
		}

		params.NoSuperblock = opts.NoSuperblock
	}

	return params, nil
}

// Format creates a dm-verity hash for a data device and returns the root hash.
// If hashDevice is the same as dataDevice, the hash will be stored on the same device.
func Format(dataDevice, hashDevice string, opts *DmverityOptions) (string, error) {
	if opts == nil {
		opts = DefaultDmverityOptions()
	}

	params, err := convertToVerityParams(opts)
	if err != nil {
		return "", fmt.Errorf("failed to convert options: %w", err)
	}

	if params.DataBlocks == 0 {
		size, err := utils.GetBlockOrFileSize(dataDevice)
		if err != nil {
			return "", fmt.Errorf("failed to get device size: %w", err)
		}
		params.DataBlocks = uint64(size / int64(params.DataBlockSize))
	}

	// IMPORTANT: This may modify params.HashAreaOffset when using superblock mode
	rootDigest, err := verity.Create(&params, dataDevice, hashDevice)
	if err != nil {
		return "", fmt.Errorf("failed to format dm-verity device: %w", err)
	}

	return fmt.Sprintf("%x", rootDigest), nil
}

// Open creates a read-only device-mapper target for transparent integrity verification.
// It supports both superblock and no-superblock modes:
//
//   - Superblock mode (opts == nil or opts.NoSuperblock == false):
//     Reads dm-verity parameters from the superblock at the specified hashOffset.
//     Only rootHash needs to be provided; all other parameters are read from the device.
//     Use hashOffset to specify where the superblock is located (required when hash tree
//     is stored in the same file as data).
//
//   - No-superblock mode (opts != nil and opts.NoSuperblock == true):
//     Uses explicitly provided parameters from opts. All dm-verity parameters must be
//     supplied programmatically since there's no superblock to read from.
func Open(dataDevice string, name string, hashDevice string, rootHash string, hashOffset uint64, opts *DmverityOptions) (string, error) {
	if rootHash == "" {
		return "", fmt.Errorf("rootHash cannot be empty")
	}

	rootDigest, err := utils.ParseRootHash(rootHash)
	if err != nil {
		return "", fmt.Errorf("invalid root hash: %w", err)
	}

	var params verity.Params

	if opts != nil && opts.NoSuperblock {
		params, err = convertToVerityParams(opts)
		if err != nil {
			return "", fmt.Errorf("failed to convert options: %w", err)
		}
	} else {
		params = verity.DefaultParams()
		params.HashAreaOffset = hashOffset
	}

	loopParams := mount.LoopParams{
		Readonly:  true,
		Autoclear: true,
	}

	dataLoop, err := mount.SetupLoop(dataDevice, loopParams)
	if err != nil {
		return "", fmt.Errorf("failed to setup loop device for data: %w", err)
	}
	dataLoopDevice := dataLoop.Name()

	var hashLoop *os.File
	var hashLoopDevice string
	if hashDevice != dataDevice {
		hashLoop, err = mount.SetupLoop(hashDevice, loopParams)
		if err != nil {
			dataLoop.Close()
			return "", fmt.Errorf("failed to setup loop device for hash: %w", err)
		}
		hashLoopDevice = hashLoop.Name()
	} else {
		hashLoopDevice = dataLoopDevice
	}

	devicePath, err := verity.Open(&params, name, dataLoopDevice, hashLoopDevice, rootDigest, "", nil)
	if err != nil {
		dataLoop.Close()
		if hashLoop != nil {
			hashLoop.Close()
		}
		return "", fmt.Errorf("failed to open dm-verity device: %w", err)
	}

	// Close file handles now that dm-verity holds a kernel reference to the loop devices.
	dataLoop.Close()
	if hashLoop != nil {
		hashLoop.Close()
	}

	return devicePath, nil
}

func Close(name string) error {
	if err := verity.Close(name); err != nil {
		return fmt.Errorf("failed to close dm-verity device: %w", err)
	}
	return nil
}

// VerifyDevice ensures an existing dm-verity device matches the expected metadata and is healthy.
func VerifyDevice(name string, rootHash string) error {
	rootDigest, err := utils.ParseRootHash(rootHash)
	if err != nil {
		return fmt.Errorf("invalid root hash: %w", err)
	}

	// Use library's Check to verify device status and root hash
	if !verity.Check(name, rootDigest) {
		return fmt.Errorf("dm-verity device %q verification failed", name)
	}

	return nil
}
