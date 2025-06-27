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

// Package dmverity provides functions for working with dm-verity for integrity verification
package dmverity

import (
	"bufio"
	"strconv"
	"strings"
)

// VeritySetupCommand represents the type of veritysetup command to execute
type VeritySetupCommand string

const (
	// FormatCommand corresponds to "veritysetup format"
	FormatCommand VeritySetupCommand = "format"
	// OpenCommand corresponds to "veritysetup open"
	OpenCommand VeritySetupCommand = "open"
	// CloseCommand corresponds to "veritysetup close"
	CloseCommand VeritySetupCommand = "close"
)

// DmverityOptions contains configuration options for dm-verity operations
type DmverityOptions struct {
	// Salt for hashing, represented as a hex string
	Salt string
	// Hash algorithm to use (default: sha256)
	HashAlgorithm string
	// Size of data blocks in bytes (default: 4096)
	DataBlockSize uint32
	// Size of hash blocks in bytes (default: 4096)
	HashBlockSize uint32
	// Number of data blocks
	DataBlocks uint64
	// Offset of hash area in bytes
	HashOffset uint64
	// Hash type (default: 1)
	HashType uint32
	// Superblock usage flag (false meaning --no-superblock)
	UseSuperblock bool
	// Debug flag
	Debug bool
	// UUID for device to use
	UUID string
}

// DefaultDmverityOptions returns a DmverityOptions struct with default values
func DefaultDmverityOptions() DmverityOptions {
	return DmverityOptions{
		HashAlgorithm: "sha256",
		DataBlockSize: 4096,
		HashBlockSize: 4096,
		HashType:      1,
	}
}

// FormatOutputInfo represents the parsed information from veritysetup format command output
type FormatOutputInfo struct {
	// Basic dm-verity options, reused from DmverityOptions
	DmverityOptions
	// Number of hash blocks in the hash area
	HashBlocks int64
	// Root hash value for verification
	RootHash string
}

// ParseFormatOutput parses the output from veritysetup format command
// and returns a structured representation of the information
func ParseFormatOutput(output string) (*FormatOutputInfo, error) {
	info := &FormatOutputInfo{}

	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()
		// Skip the header line and command echo line
		if strings.HasPrefix(line, "VERITY header") || strings.HasPrefix(line, "# veritysetup") {
			continue
		}

		parts := strings.Split(line, ":")
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		switch key {
		case "UUID":
			info.UUID = value
		case "Hash type":
			hashType, err := strconv.Atoi(value)
			if err == nil {
				info.HashType = uint32(hashType)
			}
		case "Data blocks":
			dataBlocks, err := strconv.ParseInt(value, 10, 64)
			if err == nil {
				info.DataBlocks = uint64(dataBlocks)
			}
		case "Data block size":
			dataBlockSize, err := strconv.ParseInt(value, 10, 64)
			if err == nil {
				info.DataBlockSize = uint32(dataBlockSize)
			}
		case "Hash blocks":
			hashBlocks, err := strconv.ParseInt(value, 10, 64)
			if err == nil {
				info.HashBlocks = hashBlocks
			}
		case "Hash block size":
			hashBlockSize, err := strconv.ParseInt(value, 10, 64)
			if err == nil {
				info.HashBlockSize = uint32(hashBlockSize)
			}
		case "Hash algorithm":
			info.HashAlgorithm = value
		case "Salt":
			info.Salt = value
		case "Root hash":
			info.RootHash = value
		}
	}

	return info, scanner.Err()
}
