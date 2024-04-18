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
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
	"tags.cncf.io/container-device-interface/pkg/cdi"

	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/oci"
	ctrdutil "github.com/containerd/containerd/pkg/cri/util"
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
				logrus.WithError(err).Warn(warn)
				return
			}
			p = filepath.Join("/sys/fs/cgroup", unified, "memory.swap.max")
		}
		if _, err := os.Stat(p); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				logrus.WithError(err).Warn(warn)
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
			supportsHugetlb, _ = cgroupv2HasHugetlb()
		} else {
			supportsHugetlb, _ = cgroupv1HasHugetlb()
		}
	})
	return supportsHugetlb
}

var (
	_cgroupv1HasHugetlbOnce sync.Once
	_cgroupv1HasHugetlb     bool
	_cgroupv1HasHugetlbErr  error
	_cgroupv2HasHugetlbOnce sync.Once
	_cgroupv2HasHugetlb     bool
	_cgroupv2HasHugetlbErr  error
	isUnifiedOnce           sync.Once
	isUnified               bool
)

// cgroupv1HasHugetlb returns whether the hugetlb controller is present on
// cgroup v1.
func cgroupv1HasHugetlb() (bool, error) {
	_cgroupv1HasHugetlbOnce.Do(func() {
		if _, err := os.ReadDir("/sys/fs/cgroup/hugetlb"); err != nil {
			_cgroupv1HasHugetlbErr = fmt.Errorf("readdir /sys/fs/cgroup/hugetlb: %w", err)
			_cgroupv1HasHugetlb = false
		} else {
			_cgroupv1HasHugetlbErr = nil
			_cgroupv1HasHugetlb = true
		}
	})
	return _cgroupv1HasHugetlb, _cgroupv1HasHugetlbErr
}

// cgroupv2HasHugetlb returns whether the hugetlb controller is present on
// cgroup v2.
func cgroupv2HasHugetlb() (bool, error) {
	_cgroupv2HasHugetlbOnce.Do(func() {
		controllers, err := os.ReadFile("/sys/fs/cgroup/cgroup.controllers")
		if err != nil {
			_cgroupv2HasHugetlbErr = fmt.Errorf("read /sys/fs/cgroup/cgroup.controllers: %w", err)
			return
		}
		_cgroupv2HasHugetlb = strings.Contains(string(controllers), "hugetlb")
	})
	return _cgroupv2HasHugetlb, _cgroupv2HasHugetlbErr
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
	return func(ctx context.Context, _ oci.Client, c *containers.Container, s *oci.Spec) error {
		// Add devices from CDIDevices CRI field
		var devices []string
		var err error
		for _, device := range CDIDevices {
			devices = append(devices, device.Name)
		}
		log.G(ctx).Infof("Container %v: CDI devices from CRI Config.CDIDevices: %v", c.ID, devices)

		// Add devices from CDI annotations
		_, devsFromAnnotations, err := cdi.ParseAnnotations(annotations)
		if err != nil {
			return fmt.Errorf("failed to parse CDI device annotations: %w", err)
		}

		if devsFromAnnotations != nil {
			log.G(ctx).Infof("Container %v: CDI devices from annotations: %v", c.ID, devsFromAnnotations)
			for _, deviceName := range devsFromAnnotations {
				if ctrdutil.InStringSlice(devices, deviceName) {
					// TODO: change to Warning when passing CDI devices as annotations is deprecated
					log.G(ctx).Debugf("Skipping duplicated CDI device %s", deviceName)
					continue
				}
				devices = append(devices, deviceName)
			}
			// TODO: change to Warning when passing CDI devices as annotations is deprecated
			log.G(ctx).Debug("Passing CDI devices as annotations will be deprecated soon, please use CRI CDIDevices instead")
		}

		if len(devices) == 0 {
			// No devices found, skip device injection
			return nil
		}

		if err = cdi.Refresh(); err != nil {
			// We don't consider registry refresh failure a fatal error.
			// For instance, a dynamically generated invalid CDI Spec file for
			// any particular vendor shouldn't prevent injection of devices of
			// different vendors. CDI itself knows better and it will fail the
			// injection if necessary.
			log.G(ctx).Warnf("CDI registry refresh failed: %v", err)
		}

		if _, err := cdi.InjectDevices(s, devices...); err != nil {
			return fmt.Errorf("CDI device injection failed: %w", err)
		}

		// One crucial thing to keep in mind is that CDI device injection
		// might add OCI Spec environment variables, hooks, and mounts as
		// well. Therefore it is important that none of the corresponding
		// OCI Spec fields are reset up in the call stack once we return.
		return nil
	}
}
