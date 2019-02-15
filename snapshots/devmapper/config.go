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
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
	"github.com/containerd/containerd/snapshots/devmapper/dmsetup"
	"github.com/docker/go-units"
	"github.com/hashicorp/go-multierror"
	"github.com/pkg/errors"
)

const (
	// See https://www.kernel.org/doc/Documentation/device-mapper/thin-provisioning.txt for details
	dataBlockMinSize = 128
	dataBlockMaxSize = 2097152
)

var (
	errInvalidBlockSize      = errors.Errorf("block size should be between %d and %d", dataBlockMinSize, dataBlockMaxSize)
	errInvalidBlockAlignment = errors.Errorf("block size should be multiple of %d sectors", dataBlockMinSize)
)

// Config represents device mapper configuration loaded from file.
// Size units can be specified in human-readable string format (like "32KIB", "32GB", "32Tb")
type Config struct {
	// Device snapshotter root directory for metadata
	RootPath string `toml:"root_path"`

	// Name for 'thin-pool' device to be used by snapshotter (without /dev/mapper/ prefix)
	PoolName string `toml:"pool_name"`

	// Path to data volume to be used by thin-pool
	DataDevice string `toml:"data_device"`

	// Path to metadata volume to be used by thin-pool
	MetadataDevice string `toml:"meta_device"`

	// The size of allocation chunks in data file.
	// Must be between 128 sectors (64KB) and 2097152 sectors (1GB) and a multiple of 128 sectors (64KB)
	// Block size can't be changed after pool created.
	// See https://www.kernel.org/doc/Documentation/device-mapper/thin-provisioning.txt
	DataBlockSize        string `toml:"data_block_size"`
	DataBlockSizeSectors uint32 `toml:"-"`

	// Defines how much space to allocate when creating base image for container
	BaseImageSize      string `toml:"base_image_size"`
	BaseImageSizeBytes uint64 `toml:"-"`
}

// LoadConfig reads devmapper configuration file from disk in TOML format
func LoadConfig(path string) (*Config, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, os.ErrNotExist
		}

		return nil, err
	}

	config := Config{}
	if _, err := toml.DecodeFile(path, &config); err != nil {
		return nil, errors.Wrapf(err, "failed to unmarshal data at '%s'", path)
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
	var result *multierror.Error

	if c.DataBlockSize != "" {
		if blockSize, err := units.RAMInBytes(c.DataBlockSize); err != nil {
			result = multierror.Append(result, errors.Wrapf(err, "failed to parse data block size: %q", c.DataBlockSize))
		} else {
			c.DataBlockSizeSectors = uint32(blockSize / dmsetup.SectorSize)
		}
	}

	if baseImageSize, err := units.RAMInBytes(c.BaseImageSize); err != nil {
		result = multierror.Append(result, errors.Wrapf(err, "failed to parse base image size: %q", c.BaseImageSize))
	} else {
		c.BaseImageSizeBytes = uint64(baseImageSize)
	}

	return result.ErrorOrNil()
}

// Validate makes sure configuration fields are valid
func (c *Config) Validate() error {
	var result *multierror.Error

	if c.PoolName == "" {
		result = multierror.Append(result, fmt.Errorf("pool_name is required"))
	}

	if c.RootPath == "" {
		result = multierror.Append(result, fmt.Errorf("root_path is required"))
	}

	if c.BaseImageSize == "" {
		result = multierror.Append(result, fmt.Errorf("base_image_size is required"))
	}

	// The following fields are required only if we want to create or reload pool.
	// Otherwise existing pool with 'PoolName' (prepared in advance) can be used by snapshotter.
	if c.DataDevice != "" || c.MetadataDevice != "" || c.DataBlockSize != "" || c.DataBlockSizeSectors != 0 {
		strChecks := []struct {
			field string
			name  string
		}{
			{c.DataDevice, "data_device"},
			{c.MetadataDevice, "meta_device"},
			{c.DataBlockSize, "data_block_size"},
		}

		for _, check := range strChecks {
			if check.field == "" {
				result = multierror.Append(result, errors.Errorf("%s is empty", check.name))
			}
		}

		if c.DataBlockSizeSectors < dataBlockMinSize || c.DataBlockSizeSectors > dataBlockMaxSize {
			result = multierror.Append(result, errInvalidBlockSize)
		}

		if c.DataBlockSizeSectors%dataBlockMinSize != 0 {
			result = multierror.Append(result, errInvalidBlockAlignment)
		}
	}

	return result.ErrorOrNil()
}
