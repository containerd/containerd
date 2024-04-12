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
	"sort"
	"strconv"
	"strings"
	"syscall"

	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/opencontainers/selinux/go-selinux/label"
	"github.com/sirupsen/logrus"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/oci"
	osinterface "github.com/containerd/containerd/pkg/os"
)

// WithMounts sorts and adds runtime and CRI mounts to the spec
func WithMounts(osi osinterface.OS, config *runtime.ContainerConfig, extra []*runtime.Mount, mountLabel string) oci.SpecOpts {
	return func(ctx context.Context, client oci.Client, _ *containers.Container, s *runtimespec.Spec) (err error) {
		// mergeMounts merge CRI mounts with extra mounts. If a mount destination
		// is mounted by both a CRI mount and an extra mount, the CRI mount will
		// be kept.
		var (
			criMounts = config.GetMounts()
			mounts    = append([]*runtime.Mount{}, criMounts...)
		)
		// Copy all mounts from extra mounts, except for mounts overridden by CRI.
		for _, e := range extra {
			found := false
			for _, c := range criMounts {
				if filepath.Clean(e.ContainerPath) == filepath.Clean(c.ContainerPath) {
					found = true
					break
				}
			}
			if !found {
				mounts = append(mounts, e)
			}
		}

		// Sort mounts in number of parts. This ensures that high level mounts don't
		// shadow other mounts.
		sort.Stable(orderedMounts(mounts))

		// Mount cgroup into the container as readonly, which inherits docker's behavior.
		s.Mounts = append(s.Mounts, runtimespec.Mount{
			Source:      "cgroup",
			Destination: "/sys/fs/cgroup",
			Type:        "cgroup",
			Options:     []string{"nosuid", "noexec", "nodev", "relatime", "ro"},
		})

		// Copy all mounts from default mounts, except for
		// - mounts overridden by supplied mount;
		// - all mounts under /dev if a supplied /dev is present.
		mountSet := make(map[string]struct{})
		for _, m := range mounts {
			mountSet[filepath.Clean(m.ContainerPath)] = struct{}{}
		}

		defaultMounts := s.Mounts
		s.Mounts = nil

		for _, m := range defaultMounts {
			dst := filepath.Clean(m.Destination)
			if _, ok := mountSet[dst]; ok {
				// filter out mount overridden by a supplied mount
				continue
			}
			if _, mountDev := mountSet["/dev"]; mountDev && strings.HasPrefix(dst, "/dev/") {
				// filter out everything under /dev if /dev is a supplied mount
				continue
			}
			s.Mounts = append(s.Mounts, m)
		}

		for _, mount := range mounts {
			var (
				dst = mount.GetContainerPath()
				src = mount.GetHostPath()
			)
			// Create the host path if it doesn't exist.
			// TODO(random-liu): Add CRI validation test for this case.
			if _, err := osi.Stat(src); err != nil {
				if !os.IsNotExist(err) {
					return fmt.Errorf("failed to stat %q: %w", src, err)
				}
				if err := osi.MkdirAll(src, 0755); err != nil {
					return fmt.Errorf("failed to mkdir %q: %w", src, err)
				}
			}
			// TODO(random-liu): Add cri-containerd integration test or cri validation test
			// for this.
			src, err := osi.ResolveSymbolicLink(src)
			if err != nil {
				return fmt.Errorf("failed to resolve symlink %q: %w", src, err)
			}
			if s.Linux == nil {
				s.Linux = &runtimespec.Linux{}
			}
			options := []string{"rbind"}
			switch mount.GetPropagation() {
			case runtime.MountPropagation_PROPAGATION_PRIVATE:
				options = append(options, "rprivate")
				// Since default root propagation in runc is rprivate ignore
				// setting the root propagation
			case runtime.MountPropagation_PROPAGATION_BIDIRECTIONAL:
				if err := ensureShared(src, osi.LookupMount); err != nil {
					return err
				}
				options = append(options, "rshared")
				s.Linux.RootfsPropagation = "rshared"
			case runtime.MountPropagation_PROPAGATION_HOST_TO_CONTAINER:
				if err := ensureSharedOrSlave(src, osi.LookupMount); err != nil {
					return err
				}
				options = append(options, "rslave")
				if s.Linux.RootfsPropagation != "rshared" &&
					s.Linux.RootfsPropagation != "rslave" {
					s.Linux.RootfsPropagation = "rslave"
				}
			default:
				log.G(ctx).Warnf("Unknown propagation mode for hostPath %q", mount.HostPath)
				options = append(options, "rprivate")
			}

			// NOTE(random-liu): we don't change all mounts to `ro` when root filesystem
			// is readonly. This is different from docker's behavior, but make more sense.
			if mount.GetReadonly() {
				options = append(options, "ro")
			} else {
				options = append(options, "rw")
			}

			if mount.GetSelinuxRelabel() {
				ENOTSUP := syscall.Errno(0x5f) // Linux specific error code, this branch will not execute on non Linux platforms.
				if err := label.Relabel(src, mountLabel, false); err != nil && err != ENOTSUP {
					return fmt.Errorf("relabel %q with %q failed: %w", src, mountLabel, err)
				}
			}
			if mount.UidMappings != nil || mount.GidMappings != nil {
				return fmt.Errorf("idmap mounts not yet supported, but they were requested for: %q", src)
			}

			s.Mounts = append(s.Mounts, runtimespec.Mount{
				Source:      src,
				Destination: dst,
				Type:        "bind",
				Options:     options,
			})
		}
		return nil
	}
}

// Ensure mount point on which path is mounted, is shared.
func ensureShared(path string, lookupMount func(string) (mount.Info, error)) error {
	mountInfo, err := lookupMount(path)
	if err != nil {
		return err
	}

	// Make sure source mount point is shared.
	optsSplit := strings.Split(mountInfo.Optional, " ")
	for _, opt := range optsSplit {
		if strings.HasPrefix(opt, "shared:") {
			return nil
		}
	}

	return fmt.Errorf("path %q is mounted on %q but it is not a shared mount", path, mountInfo.Mountpoint)
}

// ensure mount point on which path is mounted, is either shared or slave.
func ensureSharedOrSlave(path string, lookupMount func(string) (mount.Info, error)) error {
	mountInfo, err := lookupMount(path)
	if err != nil {
		return err
	}
	// Make sure source mount point is shared.
	optsSplit := strings.Split(mountInfo.Optional, " ")
	for _, opt := range optsSplit {
		if strings.HasPrefix(opt, "shared:") {
			return nil
		} else if strings.HasPrefix(opt, "master:") {
			return nil
		}
	}
	return fmt.Errorf("path %q is mounted on %q but it is not a shared or slave mount", path, mountInfo.Mountpoint)
}

// getDeviceUserGroupID() is used to find the right uid/gid
// value for the device node created in the container namespace.
// The runtime executes mknod() and chmod()s the created
// device with the values returned here.
//
// On Linux, uid and gid are sufficient and the user/groupname do not
// need to be resolved.
//
// TODO(mythi): In case of user namespaces, the runtime simply bind
// mounts the devices from the host. Additional logic is needed
// to check that the runtimes effective UID/GID on the host has the
// permissions to access the device node and/or the right user namespace
// mappings are created.
//
// Ref: https://github.com/kubernetes/kubernetes/issues/92211
func getDeviceUserGroupID(runAsVal *runtime.Int64Value) uint32 {
	if runAsVal != nil {
		return uint32(runAsVal.GetValue())
	}
	return 0
}

// WithDevices sets the provided devices onto the container spec
func WithDevices(osi osinterface.OS, config *runtime.ContainerConfig, enableDeviceOwnershipFromSecurityContext bool) oci.SpecOpts {
	return func(ctx context.Context, client oci.Client, c *containers.Container, s *runtimespec.Spec) (err error) {
		if s.Linux == nil {
			s.Linux = &runtimespec.Linux{}
		}
		if s.Linux.Resources == nil {
			s.Linux.Resources = &runtimespec.LinuxResources{}
		}

		oldDevices := len(s.Linux.Devices)

		for _, device := range config.GetDevices() {
			path, err := osi.ResolveSymbolicLink(device.HostPath)
			if err != nil {
				return err
			}

			o := oci.WithDevices(path, device.ContainerPath, device.Permissions)
			if err := o(ctx, client, c, s); err != nil {
				return err
			}
		}

		if enableDeviceOwnershipFromSecurityContext {
			UID := getDeviceUserGroupID(config.GetLinux().GetSecurityContext().GetRunAsUser())
			GID := getDeviceUserGroupID(config.GetLinux().GetSecurityContext().GetRunAsGroup())
			// Loop all new devices added by oci.WithDevices() to update their
			// dev.UID/dev.GID.
			//
			// non-zero UID/GID from SecurityContext is used to override host's
			// device UID/GID for the container.
			for idx := oldDevices; idx < len(s.Linux.Devices); idx++ {
				if UID != 0 {
					*s.Linux.Devices[idx].UID = UID
				}
				if GID != 0 {
					*s.Linux.Devices[idx].GID = GID
				}
			}
		}
		return nil
	}
}

// WithResources sets the provided resource restrictions
func WithResources(resources *runtime.LinuxContainerResources, tolerateMissingHugetlbController, disableHugetlbController bool) oci.SpecOpts {
	return func(ctx context.Context, client oci.Client, c *containers.Container, s *runtimespec.Spec) (err error) {
		if resources == nil {
			return nil
		}
		if s.Linux == nil {
			s.Linux = &runtimespec.Linux{}
		}
		if s.Linux.Resources == nil {
			s.Linux.Resources = &runtimespec.LinuxResources{}
		}
		if s.Linux.Resources.CPU == nil {
			s.Linux.Resources.CPU = &runtimespec.LinuxCPU{}
		}
		if s.Linux.Resources.Memory == nil {
			s.Linux.Resources.Memory = &runtimespec.LinuxMemory{}
		}
		var (
			p         = uint64(resources.GetCpuPeriod())
			q         = resources.GetCpuQuota()
			shares    = uint64(resources.GetCpuShares())
			limit     = resources.GetMemoryLimitInBytes()
			swapLimit = resources.GetMemorySwapLimitInBytes()
			hugepages = resources.GetHugepageLimits()
		)

		if p != 0 {
			s.Linux.Resources.CPU.Period = &p
		}
		if q != 0 {
			s.Linux.Resources.CPU.Quota = &q
		}
		if shares != 0 {
			s.Linux.Resources.CPU.Shares = &shares
		}
		if cpus := resources.GetCpusetCpus(); cpus != "" {
			s.Linux.Resources.CPU.Cpus = cpus
		}
		if mems := resources.GetCpusetMems(); mems != "" {
			s.Linux.Resources.CPU.Mems = resources.GetCpusetMems()
		}
		if limit != 0 {
			s.Linux.Resources.Memory.Limit = &limit
			// swap/memory limit should be equal to prevent container from swapping by default
			if swapLimit == 0 && SwapControllerAvailable() {
				s.Linux.Resources.Memory.Swap = &limit
			}
		}
		if swapLimit != 0 && SwapControllerAvailable() {
			s.Linux.Resources.Memory.Swap = &swapLimit
		}

		if !disableHugetlbController {
			if isHugetlbControllerPresent() {
				for _, limit := range hugepages {
					s.Linux.Resources.HugepageLimits = append(s.Linux.Resources.HugepageLimits, runtimespec.LinuxHugepageLimit{
						Pagesize: limit.PageSize,
						Limit:    limit.Limit,
					})
				}
			} else {
				if !tolerateMissingHugetlbController {
					return errors.New("huge pages limits are specified but hugetlb cgroup controller is missing. " +
						"Please set tolerate_missing_hugetlb_controller to `true` to ignore this error")
				}
				logrus.Warn("hugetlb cgroup controller is absent. skipping huge pages limits")
			}
		}

		if unified := resources.GetUnified(); unified != nil {
			if s.Linux.Resources.Unified == nil {
				s.Linux.Resources.Unified = make(map[string]string)
			}
			for k, v := range unified {
				s.Linux.Resources.Unified[k] = v
			}
		}
		return nil
	}
}

// WithOOMScoreAdj sets the oom score
func WithOOMScoreAdj(config *runtime.ContainerConfig, restrict bool) oci.SpecOpts {
	return func(ctx context.Context, client oci.Client, c *containers.Container, s *runtimespec.Spec) error {
		if s.Process == nil {
			s.Process = &runtimespec.Process{}
		}

		resources := config.GetLinux().GetResources()
		if resources == nil {
			return nil
		}
		adj := int(resources.GetOomScoreAdj())
		if restrict {
			var err error
			adj, err = restrictOOMScoreAdj(adj)
			if err != nil {
				return err
			}
		}
		s.Process.OOMScoreAdj = &adj
		return nil
	}
}

// WithPodOOMScoreAdj sets the oom score for the pod sandbox
func WithPodOOMScoreAdj(adj int, restrict bool) oci.SpecOpts {
	return func(ctx context.Context, client oci.Client, c *containers.Container, s *runtimespec.Spec) error {
		if s.Process == nil {
			s.Process = &runtimespec.Process{}
		}
		if restrict {
			var err error
			adj, err = restrictOOMScoreAdj(adj)
			if err != nil {
				return err
			}
		}
		s.Process.OOMScoreAdj = &adj
		return nil
	}
}

func getCurrentOOMScoreAdj() (int, error) {
	b, err := os.ReadFile("/proc/self/oom_score_adj")
	if err != nil {
		return 0, fmt.Errorf("could not get the daemon oom_score_adj: %w", err)
	}
	s := strings.TrimSpace(string(b))
	i, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("could not get the daemon oom_score_adj: %w", err)
	}
	return i, nil
}

func restrictOOMScoreAdj(preferredOOMScoreAdj int) (int, error) {
	currentOOMScoreAdj, err := getCurrentOOMScoreAdj()
	if err != nil {
		return preferredOOMScoreAdj, err
	}
	if preferredOOMScoreAdj < currentOOMScoreAdj {
		return currentOOMScoreAdj, nil
	}
	return preferredOOMScoreAdj, nil
}
