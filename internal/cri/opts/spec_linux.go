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

package opts

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/containerd/cgroups/v3"
	"golang.org/x/sys/unix"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
	"tags.cncf.io/container-device-interface/pkg/cdi"

	"github.com/containerd/log"

	"github.com/containerd/containerd/v2/core/containers"
	cdispec "github.com/containerd/containerd/v2/pkg/cdi"
	"github.com/containerd/containerd/v2/pkg/oci"
)

// Linux dependent OCI spec opts.

var (
	swapControllerAvailability     bool
	swapControllerAvailabilityOnce sync.Once
)

// SwapControllerAvailable returns true if the swap controller is available
func SwapControllerAvailable() bool {
	swapControllerAvailabilityOnce.Do(func() {
		const warn = "Failed to detect the availability of the swap controller, assuming not available"
		p := "/sys/fs/cgroup/memory/memory.memsw.limit_in_bytes"
		if cgroups.Mode() == cgroups.Unified {
			// memory.swap.max does not exist in the cgroup root, so we check /sys/fs/cgroup/<SELF>/memory.swap.max
			_, unified, err := cgroups.ParseCgroupFileUnified("/proc/self/cgroup")
			if err != nil {
				err = fmt.Errorf("failed to parse /proc/self/cgroup: %w", err)
				log.L.WithError(err).Warn(warn)
				return
			}
			p = filepath.Join("/sys/fs/cgroup", unified, "memory.swap.max")
		}
		if _, err := os.Stat(p); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				log.L.WithError(err).Warn(warn)
			}
			return
		}
		swapControllerAvailability = true
	})
	return swapControllerAvailability
}

var (
	supportsHugetlbOnce sync.Once
	supportsHugetlb     bool
)

func isHugetlbControllerPresent() bool {
	supportsHugetlbOnce.Do(func() {
		supportsHugetlb = false
		if IsCgroup2UnifiedMode() {
			supportsHugetlb = cgroupv2HasHugetlb()
		} else {
			supportsHugetlb = cgroupv1HasHugetlb()
		}
	})
	return supportsHugetlb
}

var (
	_cgroupv1HasHugetlbOnce sync.Once
	_cgroupv1HasHugetlb     bool
	_cgroupv2HasHugetlbOnce sync.Once
	_cgroupv2HasHugetlb     bool
	isUnifiedOnce           sync.Once
	isUnified               bool
)

// cgroupv1HasHugetlb returns whether the hugetlb controller is present on
// cgroup v1.
func cgroupv1HasHugetlb() bool {
	_cgroupv1HasHugetlbOnce.Do(func() {
		if _, err := os.ReadDir("/sys/fs/cgroup/hugetlb"); err != nil {
			_cgroupv1HasHugetlb = false
		} else {
			_cgroupv1HasHugetlb = true
		}
	})
	return _cgroupv1HasHugetlb
}

// cgroupv2HasHugetlb returns whether the hugetlb controller is present on
// cgroup v2.
func cgroupv2HasHugetlb() bool {
	_cgroupv2HasHugetlbOnce.Do(func() {
		controllers, err := os.ReadFile("/sys/fs/cgroup/cgroup.controllers")
		if err != nil {
			return
		}
		_cgroupv2HasHugetlb = strings.Contains(string(controllers), "hugetlb")
	})
	return _cgroupv2HasHugetlb
}

// IsCgroup2UnifiedMode returns whether we are running in cgroup v2 unified mode.
func IsCgroup2UnifiedMode() bool {
	isUnifiedOnce.Do(func() {
		var st syscall.Statfs_t
		if err := syscall.Statfs("/sys/fs/cgroup", &st); err != nil {
			panic("cannot statfs cgroup root")
		}
		isUnified = st.Type == unix.CGROUP2_SUPER_MAGIC
	})
	return isUnified
}

// WithCDI updates OCI spec with CDI content
func WithCDI(annotations map[string]string, CDIDevices []*runtime.CDIDevice) oci.SpecOpts {
	return func(ctx context.Context, client oci.Client, c *containers.Container, s *oci.Spec) error {
		seen := make(map[string]bool)
		// Add devices from CDIDevices CRI field
		var devices []string
		var err error
		for _, device := range CDIDevices {
			deviceName := device.Name
			if seen[deviceName] {
				log.G(ctx).Debugf("Skipping duplicated CDI device %s", deviceName)
				continue
			}
			devices = append(devices, deviceName)
			seen[deviceName] = true
		}
		log.G(ctx).Debugf("Container %v: CDI devices from CRI Config.CDIDevices: %v", c.ID, devices)

		// Add devices from CDI annotations
		_, devsFromAnnotations, err := cdi.ParseAnnotations(annotations)
		if err != nil {
			return fmt.Errorf("failed to parse CDI device annotations: %w", err)
		}

		if devsFromAnnotations != nil {
			log.G(ctx).Debugf("Container %v: CDI devices from annotations: %v", c.ID, devsFromAnnotations)
			for _, deviceName := range devsFromAnnotations {
				if seen[deviceName] {
					// TODO: change to Warning when passing CDI devices as annotations is deprecated
					log.G(ctx).Debugf("Skipping duplicated CDI device %s", deviceName)
					continue
				}
				devices = append(devices, deviceName)
				seen[deviceName] = true
			}
			// TODO: change to Warning when passing CDI devices as annotations is deprecated
			log.G(ctx).Debug("Passing CDI devices as annotations will be deprecated soon, please use CRI CDIDevices instead")
		}

		return cdispec.WithCDIDevices(devices...)(ctx, client, c, s)
	}
}
