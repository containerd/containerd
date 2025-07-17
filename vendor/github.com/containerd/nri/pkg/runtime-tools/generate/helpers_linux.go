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

package generate

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/moby/sys/mountinfo"
)

func getPropagation(path string) (string, error) {
	var (
		dir = filepath.Clean(path)
		mnt *mountinfo.Info
	)

	mounts, err := mountinfo.GetMounts(mountinfo.ParentsFilter(dir))
	if err != nil {
		return "", err
	}
	if len(mounts) == 0 {
		return "", fmt.Errorf("failed to get mount info for %q", path)
	}

	maxLen := 0
	for _, m := range mounts {
		if l := len(m.Mountpoint); l > maxLen {
			mnt = m
			maxLen = l
		}
	}

	for _, opt := range strings.Split(mnt.Optional, " ") {
		switch {
		case strings.HasPrefix(opt, "shared:"):
			return "rshared", nil
		case strings.HasPrefix(opt, "master:"):
			return "rslave", nil
		}
	}

	return "", nil
}

func ensurePropagation(path string, accepted ...string) error {
	prop, err := getPropagation(path)
	if err != nil {
		return err
	}

	for _, p := range accepted {
		if p == prop {
			return nil
		}
	}

	return fmt.Errorf("path %q mount propagation is %q, not %q",
		path, prop, strings.Join(accepted, " or "))
}
