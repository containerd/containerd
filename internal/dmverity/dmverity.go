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
	"bytes"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	"github.com/containerd/log"
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
	// RootHashFile specifies a file path where the root hash should be saved
	RootHashFile string
	// RootHash stores the root hash value (for metadata persistence)
	RootHash string
}

// DefaultDmverityOptions returns a DmverityOptions struct with default values
func DefaultDmverityOptions() DmverityOptions {
	return DmverityOptions{
		Salt:          "0000000000000000000000000000000000000000000000000000000000000000",
		HashAlgorithm: "sha256",
		DataBlockSize: 4096,
		HashBlockSize: 4096,
		HashType:      1,
		UseSuperblock: true,
	}
}

// ValidateOptions validates dm-verity options to ensure they are valid
// before being passed to veritysetup commands
func ValidateOptions(opts *DmverityOptions) error {
	if opts == nil {
		return fmt.Errorf("options cannot be nil")
	}

	// Validate block sizes are power of 2 (kernel requirement)
	if opts.DataBlockSize > 0 {
		if opts.DataBlockSize&(opts.DataBlockSize-1) != 0 {
			return fmt.Errorf("data block size %d must be a power of 2", opts.DataBlockSize)
		}
	}

	if opts.HashBlockSize > 0 {
		if opts.HashBlockSize&(opts.HashBlockSize-1) != 0 {
			return fmt.Errorf("hash block size %d must be a power of 2", opts.HashBlockSize)
		}
	}

	// Validate salt format (must be hex string)
	if opts.Salt != "" {
		if _, err := hex.DecodeString(opts.Salt); err != nil {
			return fmt.Errorf("salt must be a valid hex string: %w", err)
		}
	}

	return nil
}

// ValidateRootHash validates that a root hash string is in valid hexadecimal format
func ValidateRootHash(rootHash string) error {
	if rootHash == "" {
		return fmt.Errorf("root hash cannot be empty")
	}

	// Validate root hash (must be hex string)
	if _, err := hex.DecodeString(rootHash); err != nil {
		return fmt.Errorf("root hash must be a valid hex string: %w", err)
	}

	return nil
}

// ExtractRootHash extracts the root hash from veritysetup format command output.
// It first attempts to read from the root hash file (if specified in opts.RootHashFile),
// then falls back to parsing the stdout output.
//
// Note: This function expects English output when parsing stdout. The calling code
// ensures veritysetup runs with LC_ALL=C and LANG=C to prevent localization issues.
func ExtractRootHash(output string, opts *DmverityOptions) (string, error) {
	log.L.Debugf("veritysetup format output:\n%s", output)

	var rootHash string

	// Try to read from root hash file first (if specified)
	if opts != nil && opts.RootHashFile != "" {
		hashBytes, err := os.ReadFile(opts.RootHashFile)
		if err != nil {
			return "", fmt.Errorf("failed to read root hash from file %q: %w", opts.RootHashFile, err)
		}
		// Trim any whitespace/newlines
		rootHash = string(bytes.TrimSpace(hashBytes))
	} else {
		// Parse stdout output to find the root hash
		if output == "" {
			return "", fmt.Errorf("output is empty")
		}

		scanner := bufio.NewScanner(strings.NewReader(output))
		for scanner.Scan() {
			line := scanner.Text()
			// Look for the "Root hash:" line
			if strings.HasPrefix(line, "Root hash:") {
				parts := strings.Split(line, ":")
				if len(parts) == 2 {
					rootHash = strings.TrimSpace(parts[1])
					break
				}
			}
		}

		if err := scanner.Err(); err != nil {
			return "", fmt.Errorf("error scanning output: %w", err)
		}
	}

	// Validate root hash
	if err := ValidateRootHash(rootHash); err != nil {
		return "", fmt.Errorf("root hash is invalid: %w", err)
	}

	return rootHash, nil
}

// Metadata holds parsed dm-verity metadata parameters from the .dmverity file
type Metadata struct {
	RootHash      string
	HashOffset    uint64
	UseSuperblock bool
}

// MetadataPath returns the path to the dm-verity metadata file for a layer blob.
// The metadata file contains dm-verity parameters (roothash, hash-offset, use-superblock)
// in a simple key=value format.
func MetadataPath(layerBlobPath string) string {
	return layerBlobPath + ".dmverity"
}

// ParseMetadata reads and parses the .dmverity metadata file
func ParseMetadata(layerBlobPath string) (*Metadata, error) {
	metadataPath := MetadataPath(layerBlobPath)
	metadataBytes, err := os.ReadFile(metadataPath)
	if err != nil {
		return nil, fmt.Errorf("metadata file not found at %q: %w", metadataPath, err)
	}

	metadata := &Metadata{
		UseSuperblock: true, // default
	}

	lines := strings.Split(string(metadataBytes), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}

		switch key {
		case "roothash":
			metadata.RootHash = value
		case "hash-offset":
			if _, err := fmt.Sscanf(value, "%d", &metadata.HashOffset); err != nil {
				return nil, fmt.Errorf("invalid hash-offset in metadata: %w", err)
			}
		case "use-superblock":
			metadata.UseSuperblock = value == "true"
		}
	}

	// Validate required parameters
	if metadata.RootHash == "" {
		return nil, fmt.Errorf("roothash not found in dm-verity metadata")
	}
	if metadata.HashOffset == 0 {
		return nil, fmt.Errorf("hash-offset not found in dm-verity metadata")
	}

	return metadata, nil
}
