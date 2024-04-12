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
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/oci"
	osinterface "github.com/containerd/containerd/pkg/os"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// namedPipePath returns true if the given path is to a named pipe.
func namedPipePath(p string) bool {
	return strings.HasPrefix(p, `\\.\pipe\`)
}

// cleanMount returns a cleaned version of the mount path. The input is returned
// as-is if it is a named pipe path.
func cleanMount(p string) string {
	if namedPipePath(p) {
		return p
	}
	return filepath.Clean(p)
}

func parseMount(osi osinterface.OS, mount *runtime.Mount) (*runtimespec.Mount, error) {
	var (
		dst = mount.GetContainerPath()
		src = mount.GetHostPath()
	)
	// In the case of a named pipe mount on Windows, don't stat the file
	// or do other operations that open it, as that could interfere with
	// the listening process. filepath.Clean also breaks named pipe
	// paths, so don't use it.
	if !namedPipePath(src) {
		if _, err := osi.Stat(src); err != nil {
			// Create the host path if it doesn't exist. This will align
			// the behavior with the Linux implementation, but it doesn't
			// align with Docker's behavior on Windows.
			if !os.IsNotExist(err) {
				return nil, fmt.Errorf("failed to stat %q: %w", src, err)
			}
			if err := osi.MkdirAll(src, 0755); err != nil {
				return nil, fmt.Errorf("failed to mkdir %q: %w", src, err)
			}
		}
		var err error
		src, err = osi.ResolveSymbolicLink(src)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve symlink %q: %w", src, err)
		}
		// hcsshim requires clean path, especially '/' -> '\'. Additionally,
		// for the destination, absolute paths should have the C: prefix.
		src = filepath.Clean(src)

		// filepath.Clean adds a '.' at the end if the path is a
		// drive (like Z:, E: etc.). Keeping this '.' in the path
		// causes incorrect parameter error when starting the
		// container on windows.  Remove it here.
		if !(len(dst) == 2 && dst[1] == ':') {
			dst = filepath.Clean(dst)
			if dst[0] == '\\' {
				dst = "C:" + dst
			}
		} else if dst[0] == 'c' || dst[0] == 'C' {
			return nil, fmt.Errorf("destination path can not be C drive")
		}
	}

	var options []string
	// NOTE(random-liu): we don't change all mounts to `ro` when root filesystem
	// is readonly. This is different from docker's behavior, but make more sense.
	if mount.GetReadonly() {
		options = append(options, "ro")
	} else {
		options = append(options, "rw")
	}
	return &runtimespec.Mount{Source: src, Destination: dst, Options: options}, nil
}

// WithWindowsMounts sorts and adds runtime and CRI mounts to the spec for
// windows container.
func WithWindowsMounts(osi osinterface.OS, config *runtime.ContainerConfig, extra []*runtime.Mount) oci.SpecOpts {
	return func(ctx context.Context, client oci.Client, _ *containers.Container, s *runtimespec.Spec) error {
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
				if cleanMount(e.ContainerPath) == cleanMount(c.ContainerPath) {
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

		// Copy all mounts from default mounts, except for
		// mounts overridden by supplied mount;
		mountSet := make(map[string]struct{})
		for _, m := range mounts {
			mountSet[cleanMount(m.ContainerPath)] = struct{}{}
		}

		defaultMounts := s.Mounts
		s.Mounts = nil

		for _, m := range defaultMounts {
			dst := cleanMount(m.Destination)
			if _, ok := mountSet[dst]; ok {
				// filter out mount overridden by a supplied mount
				continue
			}
			s.Mounts = append(s.Mounts, m)
		}

		for _, mount := range mounts {
			parsedMount, err := parseMount(osi, mount)
			if err != nil {
				return err
			}
			s.Mounts = append(s.Mounts, *parsedMount)
		}
		return nil
	}
}

// WithWindowsResources sets the provided resource restrictions for windows.
func WithWindowsResources(resources *runtime.WindowsContainerResources) oci.SpecOpts {
	return func(ctx context.Context, client oci.Client, c *containers.Container, s *runtimespec.Spec) error {
		if resources == nil {
			return nil
		}
		if s.Windows == nil {
			s.Windows = &runtimespec.Windows{}
		}
		if s.Windows.Resources == nil {
			s.Windows.Resources = &runtimespec.WindowsResources{}
		}
		if s.Windows.Resources.Memory == nil {
			s.Windows.Resources.Memory = &runtimespec.WindowsMemoryResources{}
		}

		var (
			count  = uint64(resources.GetCpuCount())
			shares = uint16(resources.GetCpuShares())
			max    = uint16(resources.GetCpuMaximum())
			limit  = uint64(resources.GetMemoryLimitInBytes())
		)
		if s.Windows.Resources.CPU == nil && (count != 0 || shares != 0 || max != 0) {
			s.Windows.Resources.CPU = &runtimespec.WindowsCPUResources{}
		}
		if count != 0 {
			s.Windows.Resources.CPU.Count = &count
		}
		if shares != 0 {
			s.Windows.Resources.CPU.Shares = &shares
		}
		if max != 0 {
			s.Windows.Resources.CPU.Maximum = &max
		}
		if limit != 0 {
			s.Windows.Resources.Memory.Limit = &limit
		}
		return nil
	}
}

// WithWindowsDefaultSandboxShares sets the default sandbox CPU shares
func WithWindowsDefaultSandboxShares(ctx context.Context, client oci.Client, c *containers.Container, s *runtimespec.Spec) error {
	if s.Windows == nil {
		s.Windows = &runtimespec.Windows{}
	}
	if s.Windows.Resources == nil {
		s.Windows.Resources = &runtimespec.WindowsResources{}
	}
	if s.Windows.Resources.CPU == nil {
		s.Windows.Resources.CPU = &runtimespec.WindowsCPUResources{}
	}
	i := uint16(DefaultSandboxCPUshares)
	s.Windows.Resources.CPU.Shares = &i
	return nil
}

// WithWindowsCredentialSpec assigns `credentialSpec` to the
// `runtime.Spec.Windows.CredentialSpec` field.
func WithWindowsCredentialSpec(credentialSpec string) oci.SpecOpts {
	return func(ctx context.Context, client oci.Client, c *containers.Container, s *runtimespec.Spec) error {
		if s.Windows == nil {
			s.Windows = &runtimespec.Windows{}
		}
		s.Windows.CredentialSpec = credentialSpec
		return nil
	}
}

// WithWindowsDevices sets the provided devices onto the container spec
func WithWindowsDevices(config *runtime.ContainerConfig) oci.SpecOpts {
	return func(ctx context.Context, client oci.Client, c *containers.Container, s *runtimespec.Spec) (err error) {
		for _, device := range config.GetDevices() {
			if device.ContainerPath != "" {
				return fmt.Errorf("unexpected ContainerPath %s, must be empty", device.ContainerPath)
			}

			if device.Permissions != "" {
				return fmt.Errorf("unexpected Permissions %s, must be empty", device.Permissions)
			}

			hostPath := device.HostPath
			if strings.HasPrefix(hostPath, "class/") {
				hostPath = "class://" + strings.TrimPrefix(hostPath, "class/")
			}

			idType, id, ok := strings.Cut(hostPath, "://")
			if !ok {
				return fmt.Errorf("unrecognised HostPath format %v, must match IDType://ID", device.HostPath)
			}

			o := oci.WithWindowsDevice(idType, id)
			if err := o(ctx, client, c, s); err != nil {
				return fmt.Errorf("failed adding device with HostPath %v: %w", device.HostPath, err)
			}
		}
		return nil
	}
}
