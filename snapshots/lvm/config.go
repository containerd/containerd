// +build linux

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

package lvm

import (
	"strings"

	"github.com/docker/go-units"
	"github.com/pkg/errors"
)

const (
	defaultImgSize  = "10G"
	defaultFsType   = "xfs"
	defaultRootPath = "/mnt"
)

// SnapConfig will hold all the info to run the snapshotter
type SnapConfig struct {
	// Root directory of snapshotter
	RootPath string `toml:"root_path"`

	// Volume group that will hold all the thin volumes
	VgName string `toml:"vol_group"`

	// Logical volume thin pool to hold all the volumes
	ThinPool string `toml:"thin_pool"`

	// Characteristics of the volumes that we will create
	ImageSize string `toml:"img_size"`
	FsType    string `toml:"fs_type"`
}

// Validate all the necessary values exist and if not, the defaults are applied
func (c *SnapConfig) Validate(crootpath string) error {
	if c.VgName == "" || c.ThinPool == "" {
		return errors.New("Need both vol_group and thin_pool to be set")
	}

	// Trim trailing suffix in volumegroup name
	c.VgName = strings.TrimSuffix(c.VgName, "/")

	if c.RootPath == "" {
		if crootpath != "" {
			c.RootPath = crootpath
		} else {
			c.RootPath = defaultRootPath
		}
	}

	if c.ImageSize == "" {
		c.ImageSize = defaultImgSize
	} else {
		// make sure it is a consumable value
		if val, err := units.FromHumanSize(c.ImageSize); err == nil {
			c.ImageSize = units.HumanSize(float64(val))
		} else {
			return err
		}
	}

	if c.FsType == "" {
		c.FsType = defaultFsType
	}
	return nil
}
