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

package server

import (
	"fmt"
	"os"
	"sync"

	"github.com/containerd/containerd/v2/core/mount"
	kernel "github.com/containerd/containerd/v2/pkg/kernelversion"
)

var (
	volatileSupported     bool
	volatileSupportedOnce sync.Once
)

// addVolatileOptionOnImageVolumeMount adds volatile option if applicable. It
// can avoid syncfs when we clean it up.
func addVolatileOptionOnImageVolumeMount(mounts []mount.Mount) []mount.Mount {
	volatileSupportedOnce.Do(func() {
		volatileSupported, _ = kernel.GreaterEqualThan(
			kernel.KernelVersion{
				Kernel: 5, Major: 10,
			},
		)
	})

	if !volatileSupported {
		return mounts
	}

	for i, m := range mounts {
		if m.Type != "overlay" {
			continue
		}

		need := true
		for _, opt := range m.Options {
			if opt == "volatile" {
				need = false
				break
			}
		}

		if !need {
			continue
		}
		mounts[i].Options = append(mounts[i].Options, "volatile")
	}
	return mounts
}

// ensureImageVolumeMounted ensures target volume is mounted.
//
// NOTE: Currently, kubelet creates containers in pod sequencially. It won't
// cause multiple mountpoints on same target path.
func ensureImageVolumeMounted(target string) (bool, error) {
	_, err := os.Stat(target)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to stat %s: %w", target, err)
	}
	mpInfo, err := mount.Lookup(target)
	if err != nil {
		return false, fmt.Errorf("failed to check %s mountpoint: %w", target, err)
	}

	if mpInfo.Mountpoint != target {
		return false, nil
	}
	return true, nil
}
