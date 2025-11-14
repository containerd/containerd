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

	"github.com/ChengyuZhu6/veritysetup-go/pkg/dm"
	"github.com/ChengyuZhu6/veritysetup-go/pkg/utils"
	"github.com/ChengyuZhu6/veritysetup-go/pkg/verity"
	"github.com/containerd/containerd/v2/core/mount"
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

// convertToVerityParams converts DmverityOptions to verity.VerityParams
func convertToVerityParams(opts *DmverityOptions) (verity.VerityParams, error) {
	params := verity.DefaultVerityParams()

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

		// Handle salt
		if opts.Salt != "" {
			salt, saltSize, err := utils.ApplySalt(opts.Salt, 256)
			if err != nil {
				return params, fmt.Errorf("invalid salt: %w", err)
			}
			params.Salt = salt
			params.SaltSize = saltSize
		}

		// Handle UUID
		if opts.UUID != "" {
			uuidBytes, err := utils.ApplyUUID(opts.UUID, false, opts.NoSuperblock, nil)
			if err != nil {
				return params, fmt.Errorf("invalid UUID: %w", err)
			}
			params.UUID = uuidBytes
		}

		// Handle superblock flag - directly use NoSuperblock
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

	// Convert options to verity.VerityParams
	// Note: The library's VerityCreate will validate all parameters internally
	params, err := convertToVerityParams(opts)
	if err != nil {
		return "", fmt.Errorf("failed to convert options: %w", err)
	}

	// If DataBlocks is not set, calculate it from the file size
	if params.DataBlocks == 0 {
		size, err := utils.GetBlockOrFileSize(dataDevice)
		if err != nil {
			return "", fmt.Errorf("failed to get device size: %w", err)
		}
		params.DataBlocks = uint64(size / int64(params.DataBlockSize))
	}

	// Use VerityCreate to format the device
	// IMPORTANT: This may modify params.HashAreaOffset when using superblock mode
	rootDigest, err := verity.VerityCreate(&params, dataDevice, hashDevice)
	if err != nil {
		return "", fmt.Errorf("failed to format dm-verity device: %w", err)
	}

	// Convert root digest to hex string and return
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
	// Validate required parameters
	if rootHash == "" {
		return "", fmt.Errorf("rootHash cannot be empty")
	}

	// Parse root hash from hex string to bytes
	rootDigest, err := utils.ParseRootHash(rootHash)
	if err != nil {
		return "", fmt.Errorf("invalid root hash: %w", err)
	}

	var params verity.VerityParams

	// Determine mode: superblock vs no-superblock
	if opts != nil && opts.NoSuperblock {
		// No-superblock mode: use explicitly provided parameters
		params, err = convertToVerityParams(opts)
		if err != nil {
			return "", fmt.Errorf("failed to convert options: %w", err)
		}
	} else {
		// Superblock mode: read parameters from device
		params = verity.DefaultVerityParams()
		params.HashAreaOffset = hashOffset

		// Use InitParams to read superblock and extract parameters
		if err := verity.InitParams(&params, dataDevice, hashDevice); err != nil {
			return "", fmt.Errorf("failed to initialize dm-verity parameters: %w", err)
		}
	}

	loopParams := mount.LoopParams{
		Readonly:  true,
		Autoclear: false, // Don't use autoclear - dm-verity needs the loop device to stay active
	}

	dataLoop, err := mount.SetupLoop(dataDevice, loopParams)
	if err != nil {
		return "", fmt.Errorf("failed to setup loop device for data: %w", err)
	}
	dataLoopDevice := dataLoop.Name()

	var hashLoopDevice string
	if hashDevice != dataDevice {
		var hashLoop, err = mount.SetupLoop(hashDevice, loopParams)
		if err != nil {
			return "", fmt.Errorf("failed to setup loop device for hash: %w", err)
		}
		hashLoopDevice = hashLoop.Name()
	} else {
		hashLoopDevice = dataLoopDevice
	}

	openArgs := dm.OpenArgs{
		Version:        1, // verity version 1
		DataDevice:     dataLoopDevice,
		HashDevice:     hashLoopDevice,
		DataBlockSize:  params.DataBlockSize,
		HashBlockSize:  params.HashBlockSize,
		DataBlocks:     params.DataBlocks,
		HashName:       params.HashName,
		RootDigest:     rootDigest,
		Salt:           params.Salt[:params.SaltSize],
		HashStartBytes: params.HashAreaOffset,
	}

	targetParams, err := dm.BuildTargetParams(openArgs)
	if err != nil {
		return "", fmt.Errorf("failed to build target params: %w", err)
	}

	// Calculate device size in sectors (512 bytes per sector)
	sectorSize := uint64(512)
	deviceSectors := (params.DataBlocks * uint64(params.DataBlockSize)) / sectorSize

	// Open device-mapper control
	dmCtrl, err := dm.Open()
	if err != nil {
		return "", fmt.Errorf("failed to open device-mapper control: %w", err)
	}
	defer dmCtrl.Close()

	// Create the device
	if _, err := dmCtrl.CreateDevice(name); err != nil {
		return "", fmt.Errorf("failed to create dm device: %w", err)
	}

	// Load the verity table
	targets := []dm.Target{
		{
			SectorStart: 0,
			Length:      deviceSectors,
			Type:        "verity",
			Params:      targetParams,
		},
	}

	if err := dmCtrl.LoadTable(name, targets); err != nil {
		dmCtrl.RemoveDevice(name) // Clean up on error
		return "", fmt.Errorf("failed to load verity table: %w", err)
	}

	// Resume/activate the device
	if err := dmCtrl.SuspendDevice(name, false); err != nil {
		dmCtrl.RemoveDevice(name) // Clean up on error
		return "", fmt.Errorf("failed to activate dm device: %w", err)
	}

	return DevicePath(name), nil
}

// Close removes a dm-verity target and its underlying device from the device mapper table
func Close(name string) error {
	dmCtrl, err := dm.Open()
	if err != nil {
		return fmt.Errorf("failed to open device-mapper control: %w", err)
	}
	defer dmCtrl.Close()

	if err := dmCtrl.RemoveDevice(name); err != nil {
		return fmt.Errorf("failed to close dm-verity device: %w", err)
	}
	return nil
}
