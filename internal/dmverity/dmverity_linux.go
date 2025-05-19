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
	"os/exec"
)

func IsSupported() (bool, error) {
	moduleData, err := os.ReadFile("/proc/modules")
	if err != nil || !bytes.Contains(moduleData, []byte("dm_verity")) {
		return false, fmt.Errorf("dm_verity module not loaded")
	}
	if _, err := exec.LookPath("veritysetup"); err == nil {
		cmd := exec.Command("veritysetup", "--version")
		if _, err := cmd.CombinedOutput(); err != nil {
			return false, fmt.Errorf("veritysetup not found")
		}
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

	if opts.UUID != "" {
		cmdArgs = append(cmdArgs, fmt.Sprintf("--uuid=%s", opts.UUID))
	}

	if !opts.UseSuperblock {
		cmdArgs = append(cmdArgs, "--no-superblock")
	}

	if opts.HashType == 0 {
		cmdArgs = append(cmdArgs, fmt.Sprintf("--format=%d", opts.HashType))
	}

	if opts.Debug {
		cmdArgs = append(cmdArgs, "--debug")
	}

	if opts.HashAlgorithm != "" {
		cmdArgs = append(cmdArgs, "--hash="+opts.HashAlgorithm)
	}

	if opts.DataBlockSize > 0 {
		cmdArgs = append(cmdArgs, fmt.Sprintf("--data-block-size=%d", opts.DataBlockSize))
	}

	if opts.HashBlockSize > 0 {
		cmdArgs = append(cmdArgs, fmt.Sprintf("--hash-block-size=%d", opts.HashBlockSize))
	}

	if opts.DataBlocks > 0 {
		cmdArgs = append(cmdArgs, "--data-blocks", fmt.Sprintf("%d", opts.DataBlocks))
	}

	if opts.HashOffset > 0 {
		cmdArgs = append(cmdArgs, "--hash-offset", fmt.Sprintf("%d", opts.HashOffset))
	}

	if opts.Salt != "" {
		cmdArgs = append(cmdArgs, "-s", opts.Salt)
	}

	cmdArgs = append(cmdArgs, args...)

	execCmd := exec.Command("veritysetup", cmdArgs...)
	output, err := execCmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("veritysetup %s failed: %w, output: %s", cmd, err, string(output))
	}

	return string(output), nil
}

// Format creates a dm-verity hash for a data device
// If hashDevice is the same as dataDevice, the hash will be stored on the same device
func Format(dataDevice, hashDevice string, opts *DmverityOptions) (*FormatOutputInfo, error) {
	args := []string{dataDevice, hashDevice}
	output, err := actions(FormatCommand, args, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to format dm-verity device: %w, output: %s", err, output)
	}

	// Parse the output to extract structured information
	info, err := ParseFormatOutput(output)
	if err != nil {
		return nil, fmt.Errorf("failed to parse format output: %w", err)
	}

	return info, nil
}

// Open creates a read-only device-mapper target for transparent integrity verification
func Open(dataDevice string, name string, hashDevice string, rootHash string, opts *DmverityOptions) (string, error) {
	args := []string{dataDevice, name, hashDevice, rootHash}
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
