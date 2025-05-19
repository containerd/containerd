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

// runVeritySetup executes a veritysetup command with the given arguments and options
func actions(cmd VeritySetupCommand, args []string, opts *DmverityOptions) (string, error) {
	cmdArgs := []string{string(cmd)}

	if opts == nil {
		defaultOpts := DefaultDmverityOptions()
		opts = &defaultOpts
	}

	// Validate options before building command
	if err := ValidateOptions(opts); err != nil {
		return "", fmt.Errorf("invalid dm-verity options: %w", err)
	}

	// Apply options based on command type according to veritysetup man page
	switch cmd {
	case FormatCommand:
		// FORMAT options: --hash, --no-superblock, --format, --data-block-size,
		// --hash-block-size, --data-blocks, --hash-offset, --salt, --uuid, --root-hash-file
		if opts.Salt != "" {
			cmdArgs = append(cmdArgs, fmt.Sprintf("--salt=%s", opts.Salt))
		}
		if opts.HashAlgorithm != "" {
			cmdArgs = append(cmdArgs, fmt.Sprintf("--hash=%s", opts.HashAlgorithm))
		}
		if opts.DataBlockSize > 0 {
			cmdArgs = append(cmdArgs, fmt.Sprintf("--data-block-size=%d", opts.DataBlockSize))
		}
		if opts.HashBlockSize > 0 {
			cmdArgs = append(cmdArgs, fmt.Sprintf("--hash-block-size=%d", opts.HashBlockSize))
		}
		if opts.DataBlocks > 0 {
			cmdArgs = append(cmdArgs, fmt.Sprintf("--data-blocks=%d", opts.DataBlocks))
		}
		if opts.HashOffset > 0 {
			cmdArgs = append(cmdArgs, fmt.Sprintf("--hash-offset=%d", opts.HashOffset))
		}
		if opts.HashType == 0 {
			cmdArgs = append(cmdArgs, fmt.Sprintf("--format=%d", opts.HashType))
		}
		if !opts.UseSuperblock {
			cmdArgs = append(cmdArgs, "--no-superblock")
		}
		if opts.UUID != "" {
			cmdArgs = append(cmdArgs, fmt.Sprintf("--uuid=%s", opts.UUID))
		}
		if opts.RootHashFile != "" {
			cmdArgs = append(cmdArgs, fmt.Sprintf("--root-hash-file=%s", opts.RootHashFile))
		}

	case OpenCommand:
		// OPEN options: --hash-offset, --no-superblock, --root-hash-file
		// (ignoring advanced options we don't have: --ignore-corruption, --restart-on-corruption,
		// --panic-on-corruption, --ignore-zero-blocks, --check-at-most-once,
		// --root-hash-signature, --use-tasklets, --shared)
		if opts.HashOffset > 0 {
			cmdArgs = append(cmdArgs, fmt.Sprintf("--hash-offset=%d", opts.HashOffset))
		}
		if !opts.UseSuperblock {
			cmdArgs = append(cmdArgs, "--no-superblock")
		}
		if opts.RootHashFile != "" {
			cmdArgs = append(cmdArgs, fmt.Sprintf("--root-hash-file=%s", opts.RootHashFile))
		}

	case CloseCommand:
		// CLOSE has minimal options (--deferred, --cancel-deferred not implemented)
		// No options from DmverityOptions apply to close
	}

	// Debug is not command-specific, can be used with any command
	if opts.Debug {
		cmdArgs = append(cmdArgs, "--debug")
	}

	cmdArgs = append(cmdArgs, args...)

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

// Open creates a read-only device-mapper target for transparent integrity verification
func Open(dataDevice string, name string, hashDevice string, rootHash string, opts *DmverityOptions) (string, error) {
	var args []string
	// If RootHashFile is provided, use the alternate open syntax without root hash as command arg
	if opts != nil && opts.RootHashFile != "" {
		args = []string{dataDevice, name, hashDevice}
	} else {
		args = []string{dataDevice, name, hashDevice, rootHash}
	}
	output, err := actions(OpenCommand, args, opts)
	if err != nil {
		return "", fmt.Errorf("failed to open dm-verity device: %w, output: %s", err, output)
	}
	return output, nil
}

// Close removes a dm-verity target and its underlying device from the device mapper table
func Close(name string) (string, error) {
	args := []string{name}
	output, err := actions(CloseCommand, args, nil)
	if err != nil {
		return "", fmt.Errorf("failed to close dm-verity device: %w, output: %s", err, output)
	}
	return output, nil
}
