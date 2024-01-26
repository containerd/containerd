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

package podsandbox

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/containerd/containerd/v2/oci"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/opencontainers/selinux/go-selinux"
	"golang.org/x/sys/unix"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/containerd/containerd/v2/pkg/cri/annotations"
	customopts "github.com/containerd/containerd/v2/pkg/cri/opts"
	"github.com/containerd/containerd/v2/pkg/userns"
	"github.com/containerd/containerd/v2/snapshots"
)

func (c *Controller) sandboxContainerSpec(id string, config *runtime.PodSandboxConfig,
	imageConfig *imagespec.ImageConfig, nsPath string, runtimePodAnnotations []string) (_ *runtimespec.Spec, retErr error) {
	// Creates a spec Generator with the default spec.
	// TODO(random-liu): [P1] Compare the default settings with docker and containerd default.
	specOpts := []oci.SpecOpts{
		oci.WithoutRunMount,
		customopts.WithoutDefaultSecuritySettings,
		customopts.WithRelativeRoot(relativeRootfsPath),
		oci.WithEnv(imageConfig.Env),
		oci.WithRootFSReadonly(),
		oci.WithHostname(config.GetHostname()),
	}
	if imageConfig.WorkingDir != "" {
		specOpts = append(specOpts, oci.WithProcessCwd(imageConfig.WorkingDir))
	}

	if len(imageConfig.Entrypoint) == 0 && len(imageConfig.Cmd) == 0 {
		// Pause image must have entrypoint or cmd.
		return nil, fmt.Errorf("invalid empty entrypoint and cmd in image config %+v", imageConfig)
	}
	specOpts = append(specOpts, oci.WithProcessArgs(append(imageConfig.Entrypoint, imageConfig.Cmd...)...))

	// Set cgroups parent.
	if c.config.DisableCgroup {
		specOpts = append(specOpts, customopts.WithDisabledCgroups)
	} else {
		if config.GetLinux().GetCgroupParent() != "" {
			cgroupsPath := getCgroupsPath(config.GetLinux().GetCgroupParent(), id)
			specOpts = append(specOpts, oci.WithCgroup(cgroupsPath))
		}
	}

	// When cgroup parent is not set, containerd-shim will create container in a child cgroup
	// of the cgroup itself is in.
	// TODO(random-liu): [P2] Set default cgroup path if cgroup parent is not specified.

	// Set namespace options.
	var (
		securityContext = config.GetLinux().GetSecurityContext()
		nsOptions       = securityContext.GetNamespaceOptions()
	)
	if nsOptions.GetNetwork() == runtime.NamespaceMode_NODE {
		specOpts = append(specOpts, customopts.WithoutNamespace(runtimespec.NetworkNamespace))
		specOpts = append(specOpts, customopts.WithoutNamespace(runtimespec.UTSNamespace))
	} else {
		specOpts = append(specOpts, oci.WithLinuxNamespace(
			runtimespec.LinuxNamespace{
				Type: runtimespec.NetworkNamespace,
				Path: nsPath,
			}))
	}
	if nsOptions.GetPid() == runtime.NamespaceMode_NODE {
		specOpts = append(specOpts, customopts.WithoutNamespace(runtimespec.PIDNamespace))
	}
	if nsOptions.GetIpc() == runtime.NamespaceMode_NODE {
		specOpts = append(specOpts, customopts.WithoutNamespace(runtimespec.IPCNamespace))
	}

	usernsOpts := nsOptions.GetUsernsOptions()
	uids, gids, err := parseUsernsIDs(usernsOpts)
	var usernsEnabled bool
	if err != nil {
		return nil, fmt.Errorf("user namespace configuration: %w", err)
	}

	if usernsOpts != nil {
		switch mode := usernsOpts.GetMode(); mode {
		case runtime.NamespaceMode_NODE:
			specOpts = append(specOpts, customopts.WithoutNamespace(runtimespec.UserNamespace))
		case runtime.NamespaceMode_POD:
			specOpts = append(specOpts, oci.WithUserNamespace(uids, gids))
			usernsEnabled = true
		default:
			return nil, fmt.Errorf("unsupported user namespace mode: %q", mode)
		}
	}

	// It's fine to generate the spec before the sandbox /dev/shm
	// is actually created.
	sandboxDevShm := c.getSandboxDevShm(id)
	if nsOptions.GetIpc() == runtime.NamespaceMode_NODE {
		sandboxDevShm = devShm
	}
	// Remove the default /dev/shm mount from defaultMounts, it is added in oci/mounts.go.
	specOpts = append(specOpts, oci.WithoutMounts(devShm))
	// When user-namespace is enabled, the `nosuid, nodev, noexec` flags are
	// required, otherwise the remount will fail with EPERM. Just use them
	// unconditionally, they are nice to have anyways.
	specOpts = append(specOpts, oci.WithMounts([]runtimespec.Mount{
		{
			Source:      sandboxDevShm,
			Destination: devShm,
			Type:        "bind",
			Options:     []string{"rbind", "ro", "nosuid", "nodev", "noexec"},
		},
		// Add resolv.conf for katacontainers to setup the DNS of pod VM properly.
		{
			Source:      c.getResolvPath(id),
			Destination: resolvConfPath,
			Type:        "bind",
			Options:     []string{"rbind", "ro", "nosuid", "nodev", "noexec"},
		},
	}))

	processLabel, mountLabel, err := initLabelsFromOpt(securityContext.GetSelinuxOptions())
	if err != nil {
		return nil, fmt.Errorf("failed to init selinux options %+v: %w", securityContext.GetSelinuxOptions(), err)
	}
	defer func() {
		if retErr != nil {
			selinux.ReleaseLabel(processLabel)
		}
	}()

	supplementalGroups := securityContext.GetSupplementalGroups()
	specOpts = append(specOpts,
		customopts.WithSelinuxLabels(processLabel, mountLabel),
		customopts.WithSupplementalGroups(supplementalGroups),
	)

	// Add sysctls
	sysctls := config.GetLinux().GetSysctls()
	if sysctls == nil {
		sysctls = make(map[string]string)
	}
	_, ipUnprivilegedPortStart := sysctls["net.ipv4.ip_unprivileged_port_start"]
	_, pingGroupRange := sysctls["net.ipv4.ping_group_range"]
	if nsOptions.GetNetwork() != runtime.NamespaceMode_NODE {
		if c.config.EnableUnprivilegedPorts && !ipUnprivilegedPortStart {
			sysctls["net.ipv4.ip_unprivileged_port_start"] = "0"
		}
		if c.config.EnableUnprivilegedICMP && !pingGroupRange && !userns.RunningInUserNS() && !usernsEnabled {
			sysctls["net.ipv4.ping_group_range"] = "0 2147483647"
		}
	}
	specOpts = append(specOpts, customopts.WithSysctls(sysctls))

	// Note: LinuxSandboxSecurityContext does not currently provide an apparmor profile

	if !c.config.DisableCgroup {
		specOpts = append(specOpts, customopts.WithDefaultSandboxShares)
	}

	if res := config.GetLinux().GetResources(); res != nil {
		specOpts = append(specOpts,
			customopts.WithAnnotation(annotations.SandboxCPUPeriod, strconv.FormatInt(res.CpuPeriod, 10)),
			customopts.WithAnnotation(annotations.SandboxCPUQuota, strconv.FormatInt(res.CpuQuota, 10)),
			customopts.WithAnnotation(annotations.SandboxCPUShares, strconv.FormatInt(res.CpuShares, 10)),
			customopts.WithAnnotation(annotations.SandboxMem, strconv.FormatInt(res.MemoryLimitInBytes, 10)))
	}

	specOpts = append(specOpts, customopts.WithPodOOMScoreAdj(int(defaultSandboxOOMAdj), c.config.RestrictOOMScoreAdj))

	for pKey, pValue := range getPassthroughAnnotations(config.Annotations,
		runtimePodAnnotations) {
		specOpts = append(specOpts, customopts.WithAnnotation(pKey, pValue))
	}

	specOpts = append(specOpts, annotations.DefaultCRIAnnotations(id, "", "", config, true)...)

	return c.runtimeSpec(id, "", specOpts...)
}

// sandboxContainerSpecOpts generates OCI spec options for
// the sandbox container.
func (c *Controller) sandboxContainerSpecOpts(config *runtime.PodSandboxConfig, imageConfig *imagespec.ImageConfig) ([]oci.SpecOpts, error) {
	var (
		securityContext = config.GetLinux().GetSecurityContext()
		specOpts        []oci.SpecOpts
		err             error
	)
	ssp := securityContext.GetSeccomp()
	if ssp == nil {
		ssp, err = generateSeccompSecurityProfile(
			securityContext.GetSeccompProfilePath(), //nolint:staticcheck // Deprecated but we don't want to remove yet
			c.config.UnsetSeccompProfile)
		if err != nil {
			return nil, fmt.Errorf("failed to generate seccomp spec opts: %w", err)
		}
	}
	seccompSpecOpts, err := c.generateSeccompSpecOpts(
		ssp,
		securityContext.GetPrivileged(),
		c.seccompEnabled())
	if err != nil {
		return nil, fmt.Errorf("failed to generate seccomp spec opts: %w", err)
	}
	if seccompSpecOpts != nil {
		specOpts = append(specOpts, seccompSpecOpts)
	}

	userstr, err := generateUserString(
		"",
		securityContext.GetRunAsUser(),
		securityContext.GetRunAsGroup(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to generate user string: %w", err)
	}
	if userstr == "" {
		// Lastly, since no user override was passed via CRI try to set via OCI
		// Image
		userstr = imageConfig.User
	}
	if userstr != "" {
		specOpts = append(specOpts, oci.WithUser(userstr))
	}
	return specOpts, nil
}

// setupSandboxFiles sets up necessary sandbox files including /dev/shm, /etc/hosts,
// /etc/resolv.conf and /etc/hostname.
func (c *Controller) setupSandboxFiles(id string, config *runtime.PodSandboxConfig) error {
	sandboxEtcHostname := c.getSandboxHostname(id)
	hostname := config.GetHostname()
	if hostname == "" {
		var err error
		hostname, err = c.os.Hostname()
		if err != nil {
			return fmt.Errorf("failed to get hostname: %w", err)
		}
	}
	if err := c.os.WriteFile(sandboxEtcHostname, []byte(hostname+"\n"), 0644); err != nil {
		return fmt.Errorf("failed to write hostname to %q: %w", sandboxEtcHostname, err)
	}

	// TODO(random-liu): Consider whether we should maintain /etc/hosts and /etc/resolv.conf in kubelet.
	sandboxEtcHosts := c.getSandboxHosts(id)
	if err := c.os.CopyFile(etcHosts, sandboxEtcHosts, 0644); err != nil {
		return fmt.Errorf("failed to generate sandbox hosts file %q: %w", sandboxEtcHosts, err)
	}

	// Set DNS options. Maintain a resolv.conf for the sandbox.
	var err error
	resolvContent := ""
	if dnsConfig := config.GetDnsConfig(); dnsConfig != nil {
		resolvContent, err = parseDNSOptions(dnsConfig.Servers, dnsConfig.Searches, dnsConfig.Options)
		if err != nil {
			return fmt.Errorf("failed to parse sandbox DNSConfig %+v: %w", dnsConfig, err)
		}
	}
	resolvPath := c.getResolvPath(id)
	if resolvContent == "" {
		// copy host's resolv.conf to resolvPath
		err = c.os.CopyFile(resolvConfPath, resolvPath, 0644)
		if err != nil {
			return fmt.Errorf("failed to copy host's resolv.conf to %q: %w", resolvPath, err)
		}
	} else {
		err = c.os.WriteFile(resolvPath, []byte(resolvContent), 0644)
		if err != nil {
			return fmt.Errorf("failed to write resolv content to %q: %w", resolvPath, err)
		}
	}

	// Setup sandbox /dev/shm.
	if config.GetLinux().GetSecurityContext().GetNamespaceOptions().GetIpc() == runtime.NamespaceMode_NODE {
		if _, err := c.os.Stat(devShm); err != nil {
			return fmt.Errorf("host %q is not available for host ipc: %w", devShm, err)
		}
	} else {
		sandboxDevShm := c.getSandboxDevShm(id)
		if err := c.os.MkdirAll(sandboxDevShm, 0700); err != nil {
			return fmt.Errorf("failed to create sandbox shm: %w", err)
		}
		shmproperty := fmt.Sprintf("mode=1777,size=%d", defaultShmSize)
		if err := c.os.Mount("shm", sandboxDevShm, "tmpfs", uintptr(unix.MS_NOEXEC|unix.MS_NOSUID|unix.MS_NODEV), shmproperty); err != nil {
			return fmt.Errorf("failed to mount sandbox shm: %w", err)
		}
	}

	return nil
}

// parseDNSOptions parse DNS options into resolv.conf format content,
// if none option is specified, will return empty with no error.
func parseDNSOptions(servers, searches, options []string) (string, error) {
	resolvContent := ""

	if len(searches) > 0 {
		resolvContent += fmt.Sprintf("search %s\n", strings.Join(searches, " "))
	}

	if len(servers) > 0 {
		resolvContent += fmt.Sprintf("nameserver %s\n", strings.Join(servers, "\nnameserver "))
	}

	if len(options) > 0 {
		resolvContent += fmt.Sprintf("options %s\n", strings.Join(options, " "))
	}

	return resolvContent, nil
}

// cleanupSandboxFiles unmount some sandbox files, we rely on the removal of sandbox root directory to
// remove these files. Unmount should *NOT* return error if the mount point is already unmounted.
func (c *Controller) cleanupSandboxFiles(id string, config *runtime.PodSandboxConfig) error {
	if config.GetLinux().GetSecurityContext().GetNamespaceOptions().GetIpc() != runtime.NamespaceMode_NODE {
		path, err := c.os.FollowSymlinkInScope(c.getSandboxDevShm(id), "/")
		if err != nil {
			return fmt.Errorf("failed to follow symlink: %w", err)
		}
		if err := c.os.Unmount(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to unmount %q: %w", path, err)
		}
	}
	return nil
}

// sandboxSnapshotterOpts generates any platform specific snapshotter options
// for a sandbox container.
func sandboxSnapshotterOpts(config *runtime.PodSandboxConfig) ([]snapshots.Opt, error) {
	nsOpts := config.GetLinux().GetSecurityContext().GetNamespaceOptions()
	return snapshotterRemapOpts(nsOpts)
}
