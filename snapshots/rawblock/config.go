// +build linux,!no_rawblock

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

package rawblock

import (
	"os/exec"

	"github.com/pkg/errors"
)

const (
	defaultImageSizeMB = 1024
	defaultFsType      = "ext4"
)

// SnapshotterConfig allows users to set rawblock snapshotter options.
type SnapshotterConfig struct {
	RootPath string   `toml:"root_path"`
	SizeMB   uint32   `toml:"size_mb"`
	FsType   string   `toml:"fs_type"`
	Options  []string `toml:"options"`
}

func (c *SnapshotterConfig) setDefaults(contextRootPath string) {
	if c.RootPath == "" {
		c.RootPath = contextRootPath
	}

	if c.SizeMB == 0 {
		c.SizeMB = defaultImageSizeMB
	}

	if c.FsType == "" {
		c.FsType = defaultFsType
	}

	// XFS needs nouuid or it can't mount filesystems with the same fs uuid
	if c.FsType == "xfs" {
		var found bool
		for _, opt := range c.Options {
			if opt == "nouuid" {
				found = true
				break
			}
		}
		if !found {
			c.Options = append(c.Options, "nouuid")
		}
	}
}

func (c *SnapshotterConfig) validate() error {
	if c.FsType != "ext4" && c.FsType != "xfs" {
		return errors.New("rawblock snapshotter only supports ext4 and xfs image fstype")
	}

	mkfsBin := "mkfs." + c.FsType
	_, err := exec.LookPath(mkfsBin)
	if err != nil {
		return errors.Wrapf(err, "cannot find mkfs binary %s", mkfsBin)
	}

	return nil
}
