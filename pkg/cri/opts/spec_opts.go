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
	"strings"

	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/oci"
	"github.com/containerd/containerd/pkg/cri/util"
)

// DefaultSandboxCPUshares is default cpu shares for sandbox container.
// TODO(windows): Revisit cpu shares for windows (https://github.com/containerd/cri/issues/1297)
const DefaultSandboxCPUshares = 2

// WithRelativeRoot sets the root for the container
func WithRelativeRoot(root string) oci.SpecOpts {
	return func(ctx context.Context, client oci.Client, c *containers.Container, s *runtimespec.Spec) (err error) {
		if s.Root == nil {
			s.Root = &runtimespec.Root{}
		}
		s.Root.Path = root
		return nil
	}
}

// WithoutRoot sets the root to nil for the container.
func WithoutRoot(ctx context.Context, client oci.Client, c *containers.Container, s *runtimespec.Spec) error {
	s.Root = nil
	return nil
}

// WithProcessArgs sets the process args on the spec based on the image and runtime config
func WithProcessArgs(config *runtime.ContainerConfig, image *imagespec.ImageConfig) oci.SpecOpts {
	return func(ctx context.Context, client oci.Client, c *containers.Container, s *runtimespec.Spec) (err error) {
		command, args := config.GetCommand(), config.GetArgs()
		// The following logic is migrated from https://github.com/moby/moby/blob/master/daemon/commit.go
		// TODO(random-liu): Clearly define the commands overwrite behavior.
		if len(command) == 0 {
			// Copy array to avoid data race.
			if len(args) == 0 {
				args = append([]string{}, image.Cmd...)
			}
			if command == nil {
				if !(len(image.Entrypoint) == 1 && image.Entrypoint[0] == "") {
					command = append([]string{}, image.Entrypoint...)
				}
			}
		}
		if len(command) == 0 && len(args) == 0 {
			return errors.New("no command specified")
		}
		return oci.WithProcessArgs(append(command, args...)...)(ctx, client, c, s)
	}
}

// mounts defines how to sort runtime.Mount.
// This is the same with the Docker implementation:
//
//	https://github.com/moby/moby/blob/17.05.x/daemon/volumes.go#L26
type orderedMounts []*runtime.Mount

// Len returns the number of mounts. Used in sorting.
func (m orderedMounts) Len() int {
	return len(m)
}

// Less returns true if the number of parts (a/b/c would be 3 parts) in the
// mount indexed by parameter 1 is less than that of the mount indexed by
// parameter 2. Used in sorting.
func (m orderedMounts) Less(i, j int) bool {
	return m.parts(i) < m.parts(j)
}

// Swap swaps two items in an array of mounts. Used in sorting
func (m orderedMounts) Swap(i, j int) {
	m[i], m[j] = m[j], m[i]
}

// parts returns the number of parts in the destination of a mount. Used in sorting.
func (m orderedMounts) parts(i int) int {
	return strings.Count(filepath.Clean(m[i].ContainerPath), string(os.PathSeparator))
}

// WithAnnotation sets the provided annotation
func WithAnnotation(k, v string) oci.SpecOpts {
	return func(ctx context.Context, client oci.Client, c *containers.Container, s *runtimespec.Spec) error {
		if s.Annotations == nil {
			s.Annotations = make(map[string]string)
		}
		s.Annotations[k] = v
		return nil
	}
}

// WithAdditionalGIDs adds any additional groups listed for a particular user in the
// /etc/groups file of the image's root filesystem to the OCI spec's additionalGids array.
func WithAdditionalGIDs(userstr string) oci.SpecOpts {
	return func(ctx context.Context, client oci.Client, c *containers.Container, s *runtimespec.Spec) (err error) {
		if s.Process == nil {
			s.Process = &runtimespec.Process{}
		}
		gids := s.Process.User.AdditionalGids
		if err := oci.WithAdditionalGIDs(userstr)(ctx, client, c, s); err != nil {
			return err
		}
		// Merge existing gids and new gids.
		s.Process.User.AdditionalGids = mergeGids(s.Process.User.AdditionalGids, gids)
		return nil
	}
}

func mergeGids(gids1, gids2 []uint32) []uint32 {
	gidsMap := make(map[uint32]struct{})
	for _, gid1 := range gids1 {
		gidsMap[gid1] = struct{}{}
	}
	for _, gid2 := range gids2 {
		gidsMap[gid2] = struct{}{}
	}
	var gids []uint32
	for gid := range gidsMap {
		gids = append(gids, gid)
	}
	sort.Slice(gids, func(i, j int) bool { return gids[i] < gids[j] })
	return gids
}

// WithoutDefaultSecuritySettings removes the default security settings generated on a spec
func WithoutDefaultSecuritySettings(_ context.Context, _ oci.Client, c *containers.Container, s *runtimespec.Spec) error {
	if s.Process == nil {
		s.Process = &runtimespec.Process{}
	}
	// Make sure no default seccomp/apparmor is specified
	s.Process.ApparmorProfile = ""
	if s.Linux != nil {
		s.Linux.Seccomp = nil
	}
	// Remove default rlimits (See issue #515)
	s.Process.Rlimits = nil
	return nil
}

// WithCapabilities sets the provided capabilities from the security context
func WithCapabilities(sc *runtime.LinuxContainerSecurityContext, allCaps []string) oci.SpecOpts {
	capabilities := sc.GetCapabilities()
	if capabilities == nil {
		return nullOpt
	}

	var opts []oci.SpecOpts
	// Add/drop all capabilities if "all" is specified, so that
	// following individual add/drop could still work. E.g.
	// AddCapabilities: []string{"ALL"}, DropCapabilities: []string{"CHOWN"}
	// will be all capabilities without `CAP_CHOWN`.
	if util.InStringSlice(capabilities.GetAddCapabilities(), "ALL") {
		opts = append(opts, oci.WithCapabilities(allCaps))
	}
	if util.InStringSlice(capabilities.GetDropCapabilities(), "ALL") {
		opts = append(opts, oci.WithCapabilities(nil))
	}

	var caps []string
	for _, c := range capabilities.GetAddCapabilities() {
		if strings.ToUpper(c) == "ALL" {
			continue
		}
		// Capabilities in CRI doesn't have `CAP_` prefix, so add it.
		caps = append(caps, "CAP_"+strings.ToUpper(c))
	}
	opts = append(opts, oci.WithAddedCapabilities(caps))

	caps = []string{}
	for _, c := range capabilities.GetDropCapabilities() {
		if strings.ToUpper(c) == "ALL" {
			continue
		}
		caps = append(caps, "CAP_"+strings.ToUpper(c))
	}
	opts = append(opts, oci.WithDroppedCapabilities(caps))
	return oci.Compose(opts...)
}

func nullOpt(_ context.Context, _ oci.Client, _ *containers.Container, _ *runtimespec.Spec) error {
	return nil
}

// WithoutAmbientCaps removes the ambient caps from the spec
func WithoutAmbientCaps(_ context.Context, _ oci.Client, c *containers.Container, s *runtimespec.Spec) error {
	if s.Process == nil {
		s.Process = &runtimespec.Process{}
	}
	if s.Process.Capabilities == nil {
		s.Process.Capabilities = &runtimespec.LinuxCapabilities{}
	}
	s.Process.Capabilities.Ambient = nil
	return nil
}

// WithDisabledCgroups clears the Cgroups Path from the spec
func WithDisabledCgroups(_ context.Context, _ oci.Client, c *containers.Container, s *runtimespec.Spec) error {
	if s.Linux == nil {
		s.Linux = &runtimespec.Linux{}
	}
	s.Linux.CgroupsPath = ""
	return nil
}

// WithSelinuxLabels sets the mount and process labels
func WithSelinuxLabels(process, mount string) oci.SpecOpts {
	return func(ctx context.Context, client oci.Client, c *containers.Container, s *runtimespec.Spec) (err error) {
		if s.Linux == nil {
			s.Linux = &runtimespec.Linux{}
		}
		if s.Process == nil {
			s.Process = &runtimespec.Process{}
		}
		s.Linux.MountLabel = mount
		s.Process.SelinuxLabel = process
		return nil
	}
}

// WithSysctls sets the provided sysctls onto the spec
func WithSysctls(sysctls map[string]string) oci.SpecOpts {
	return func(ctx context.Context, client oci.Client, c *containers.Container, s *runtimespec.Spec) error {
		if s.Linux == nil {
			s.Linux = &runtimespec.Linux{}
		}
		if s.Linux.Sysctl == nil {
			s.Linux.Sysctl = make(map[string]string)
		}
		for k, v := range sysctls {
			s.Linux.Sysctl[k] = v
		}
		return nil
	}
}

// WithSupplementalGroups sets the supplemental groups for the process
func WithSupplementalGroups(groups []int64) oci.SpecOpts {
	return func(ctx context.Context, client oci.Client, c *containers.Container, s *runtimespec.Spec) error {
		if s.Process == nil {
			s.Process = &runtimespec.Process{}
		}
		var guids []uint32
		for _, g := range groups {
			guids = append(guids, uint32(g))
		}
		s.Process.User.AdditionalGids = mergeGids(s.Process.User.AdditionalGids, guids)
		return nil
	}
}

// WithDefaultSandboxShares sets the default sandbox CPU shares
func WithDefaultSandboxShares(ctx context.Context, client oci.Client, c *containers.Container, s *runtimespec.Spec) error {
	if s.Linux == nil {
		s.Linux = &runtimespec.Linux{}
	}
	if s.Linux.Resources == nil {
		s.Linux.Resources = &runtimespec.LinuxResources{}
	}
	if s.Linux.Resources.CPU == nil {
		s.Linux.Resources.CPU = &runtimespec.LinuxCPU{}
	}
	i := uint64(DefaultSandboxCPUshares)
	s.Linux.Resources.CPU.Shares = &i
	return nil
}

// WithoutNamespace removes the provided namespace
func WithoutNamespace(t runtimespec.LinuxNamespaceType) oci.SpecOpts {
	return func(ctx context.Context, client oci.Client, c *containers.Container, s *runtimespec.Spec) error {
		if s.Linux == nil {
			return nil
		}
		var namespaces []runtimespec.LinuxNamespace
		for i, ns := range s.Linux.Namespaces {
			if ns.Type != t {
				namespaces = append(namespaces, s.Linux.Namespaces[i])
			}
		}
		s.Linux.Namespaces = namespaces
		return nil
	}
}

// WithPodNamespaces sets the pod namespaces for the container
func WithPodNamespaces(config *runtime.LinuxContainerSecurityContext, sandboxPid uint32, targetPid uint32, uids, gids []runtimespec.LinuxIDMapping) oci.SpecOpts {
	namespaces := config.GetNamespaceOptions()

	opts := []oci.SpecOpts{
		oci.WithLinuxNamespace(runtimespec.LinuxNamespace{Type: runtimespec.NetworkNamespace, Path: GetNetworkNamespace(sandboxPid)}),
		oci.WithLinuxNamespace(runtimespec.LinuxNamespace{Type: runtimespec.IPCNamespace, Path: GetIPCNamespace(sandboxPid)}),
		oci.WithLinuxNamespace(runtimespec.LinuxNamespace{Type: runtimespec.UTSNamespace, Path: GetUTSNamespace(sandboxPid)}),
	}
	if namespaces.GetPid() != runtime.NamespaceMode_CONTAINER {
		opts = append(opts, oci.WithLinuxNamespace(runtimespec.LinuxNamespace{Type: runtimespec.PIDNamespace, Path: GetPIDNamespace(targetPid)}))
	}

	if namespaces.GetUsernsOptions() != nil {
		switch namespaces.GetUsernsOptions().GetMode() {
		case runtime.NamespaceMode_NODE:
			// Nothing to do. Not adding userns field uses the node userns.
		case runtime.NamespaceMode_POD:
			opts = append(opts, oci.WithLinuxNamespace(runtimespec.LinuxNamespace{Type: runtimespec.UserNamespace, Path: GetUserNamespace(sandboxPid)}))
			opts = append(opts, oci.WithUserNamespace(uids, gids))
		}
	}

	return oci.Compose(opts...)
}

const (
	// netNSFormat is the format of network namespace of a process.
	netNSFormat = "/proc/%v/ns/net"
	// ipcNSFormat is the format of ipc namespace of a process.
	ipcNSFormat = "/proc/%v/ns/ipc"
	// utsNSFormat is the format of uts namespace of a process.
	utsNSFormat = "/proc/%v/ns/uts"
	// pidNSFormat is the format of pid namespace of a process.
	pidNSFormat = "/proc/%v/ns/pid"
	// userNSFormat is the format of user namespace of a process.
	userNSFormat = "/proc/%v/ns/user"
)

// GetNetworkNamespace returns the network namespace of a process.
func GetNetworkNamespace(pid uint32) string {
	return fmt.Sprintf(netNSFormat, pid)
}

// GetIPCNamespace returns the ipc namespace of a process.
func GetIPCNamespace(pid uint32) string {
	return fmt.Sprintf(ipcNSFormat, pid)
}

// GetUTSNamespace returns the uts namespace of a process.
func GetUTSNamespace(pid uint32) string {
	return fmt.Sprintf(utsNSFormat, pid)
}

// GetPIDNamespace returns the pid namespace of a process.
func GetPIDNamespace(pid uint32) string {
	return fmt.Sprintf(pidNSFormat, pid)
}

// GetUserNamespace returns the user namespace of a process.
func GetUserNamespace(pid uint32) string {
	return fmt.Sprintf(userNSFormat, pid)
}
