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
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"
)

const (
	// veritysetupTimeout is the maximum time allowed for a veritysetup command to complete
	// Format operations can take time for large devices, but should complete within 5 minutes
	veritysetupTimeout = 5 * time.Minute
)

func IsSupported() (bool, error) {
	moduleData, err := os.ReadFile("/proc/modules")
	if err != nil {
		return false, fmt.Errorf("failed to read /proc/modules: %w", err)
	}
	if !bytes.Contains(moduleData, []byte("dm_verity")) {
		return false, fmt.Errorf("dm_verity module not loaded")
	}

	veritysetupPath, err := exec.LookPath("veritysetup")
	if err != nil {
		return false, fmt.Errorf("veritysetup not found in PATH: %w", err)
	}

	cmd := exec.Command(veritysetupPath, "--version")
	if _, err := cmd.CombinedOutput(); err != nil {
		return false, fmt.Errorf("veritysetup not functional: %w", err)
	}

	return true, nil
}

// actions executes a veritysetup command with the given arguments and options
func actions(cmd VeritySetupCommand, args []string, opts *DmverityOptions) (string, error) {
	if opts == nil {
		opts = DefaultDmverityOptions()
	}

	if err := ValidateOptions(opts); err != nil {
		return "", fmt.Errorf("invalid dm-verity options: %w", err)
	}

	// Build command arguments
	cmdArgs := []string{string(cmd)}

	// Helper to add option if value is non-empty/non-zero
	addOpt := func(flag string, value interface{}) {
		switch v := value.(type) {
		case string:
			if v != "" {
				cmdArgs = append(cmdArgs, fmt.Sprintf("%s=%s", flag, v))
			}
		case uint32:
			if v > 0 {
				cmdArgs = append(cmdArgs, fmt.Sprintf("%s=%d", flag, v))
			}
		case uint64:
			if v > 0 {
				cmdArgs = append(cmdArgs, fmt.Sprintf("%s=%d", flag, v))
			}
		case bool:
			if v {
				cmdArgs = append(cmdArgs, flag)
			}
		}
	}

	// Add options based on what's set in opts
	addOpt("--salt", opts.Salt)
	addOpt("--hash", opts.HashAlgorithm)
	addOpt("--data-block-size", opts.DataBlockSize)
	addOpt("--hash-block-size", opts.HashBlockSize)
	addOpt("--data-blocks", opts.DataBlocks)
	addOpt("--hash-offset", opts.HashOffset)
	addOpt("--no-superblock", opts.NoSuperblock)
	addOpt("--uuid", opts.UUID)
	addOpt("--root-hash-file", opts.RootHashFile)

	// Append positional arguments
	cmdArgs = append(cmdArgs, args...)

	// Execute command
	ctx, cancel := context.WithTimeout(context.Background(), veritysetupTimeout)
	defer cancel()

	execCmd := exec.CommandContext(ctx, "veritysetup", cmdArgs...)
	// Force C locale to ensure consistent, non-localized output that we can parse reliably
	// This prevents localization issues where field names like "Root hash", "Salt", etc.
	// might be translated to other languages, breaking our text parsing
	execCmd.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	output, err := execCmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("veritysetup %s failed: %w, output: %s", cmd, err, string(output))
	}

	return string(output), nil
}

// Format creates a dm-verity hash for a data device and returns the root hash.
// If hashDevice is the same as dataDevice, the hash will be stored on the same device.
func Format(dataDevice, hashDevice string, opts *DmverityOptions) (string, error) {
	args := []string{dataDevice, hashDevice}
	output, err := actions(FormatCommand, args, opts)
	if err != nil {
		return "", fmt.Errorf("failed to format dm-verity device: %w, output: %s", err, output)
	}

	// Extract the root hash from the format output
	// Pass opts so ExtractRootHash can read root hash from file if RootHashFile was specified
	rootHash, err := ExtractRootHash(output, opts)
	if err != nil {
		return "", fmt.Errorf("failed to extract root hash: %w", err)
	}

	return rootHash, nil
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
	// When using RootHashFile, veritysetup reads the root hash from the file
	if rootHash == "" && (opts == nil || opts.RootHashFile == "") {
		return "", fmt.Errorf("rootHash cannot be empty")
	}

	// Build options if not provided
	if opts == nil {
		opts = &DmverityOptions{
			HashOffset: hashOffset,
		}
	} else if hashOffset > 0 {
		opts.HashOffset = hashOffset
	}

	var args []string
	// If RootHashFile is provided, use the alternate open syntax without root hash as command arg
	if opts.RootHashFile != "" {
		args = []string{dataDevice, name, hashDevice}
	} else {
		args = []string{dataDevice, name, hashDevice, rootHash}
	}
	output, err := actions(OpenCommand, args, opts)
	if err != nil {
		return "", fmt.Errorf("failed to open dm-verity device: %w, output: %s", err, output)
	}

	// Return the device path
	return DevicePath(name), nil
}

// Close removes a dm-verity target and its underlying device from the device mapper table
func Close(name string) error {
	args := []string{name}
	output, err := actions(CloseCommand, args, nil)
	if err != nil {
		return fmt.Errorf("failed to close dm-verity device: %w, output: %s", err, output)
	}
	return nil
}
