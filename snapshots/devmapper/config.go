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

package devmapper

import (
	"errors"
	"fmt"
	"os"

	"github.com/docker/go-units"
	"github.com/pelletier/go-toml/v2"
)

// Config represents device mapper configuration loaded from file.
// Size units can be specified in human-readable string format (like "32KIB", "32GB", "32Tb")
type Config struct {
	// Device snapshotter root directory for metadata
	RootPath string `toml:"root_path"`

	// Name for 'thin-pool' device to be used by snapshotter (without /dev/mapper/ prefix)
	PoolName string `toml:"pool_name"`

	// Defines how much space to allocate when creating base image for container
	BaseImageSize      string `toml:"base_image_size"`
	BaseImageSizeBytes uint64 `toml:"-"`

	// Flag to async remove device using Cleanup() callback in snapshots GC
	AsyncRemove bool `toml:"async_remove"`

	// Whether to discard blocks when removing a thin device.
	DiscardBlocks bool `toml:"discard_blocks"`

	// Defines file system to use for snapshout device mount. Defaults to "ext4"
	FileSystemType fsType `toml:"fs_type"`

	// Defines optional file system options passed through config file
	FsOptions string `toml:"fs_options"`
}

// LoadConfig reads devmapper configuration file from disk in TOML format
func LoadConfig(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, os.ErrNotExist
		}

		return nil, err

	}
	defer f.Close()

	config := Config{}
	if err := toml.NewDecoder(f).Decode(&config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal devmapper TOML: %w", err)
	}

	if err := config.parse(); err != nil {
		return nil, err
	}

	if err := config.Validate(); err != nil {
		return nil, err
	}

	return &config, nil
}

func (c *Config) parse() error {
	baseImageSize, err := units.RAMInBytes(c.BaseImageSize)
	if err != nil {
		return fmt.Errorf("failed to parse base image size: '%s': %w", c.BaseImageSize, err)
	}

	if c.FileSystemType == "" {
		c.FileSystemType = fsTypeExt4
	}

	c.BaseImageSizeBytes = uint64(baseImageSize)
	return nil
}

// Validate makes sure configuration fields are valid
func (c *Config) Validate() error {
	var result []error

	if c.PoolName == "" {
		result = append(result, fmt.Errorf("pool_name is required"))
	}

	if c.RootPath == "" {
		result = append(result, fmt.Errorf("root_path is required"))
	}

	if c.BaseImageSize == "" {
		result = append(result, fmt.Errorf("base_image_size is required"))
	}

	if c.FileSystemType != "" {
		switch c.FileSystemType {
		case fsTypeExt4, fsTypeXFS, fsTypeExt2:
		default:
			result = append(result, fmt.Errorf("unsupported Filesystem Type: %q", c.FileSystemType))
		}
	} else {
		result = append(result, fmt.Errorf("filesystem type cannot be empty"))
	}

	return errors.Join(result...)
}
