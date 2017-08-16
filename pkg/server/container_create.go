/*
Copyright 2017 The Kubernetes Authors.

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
	"strings"
	"time"

	"github.com/containerd/containerd"
	"github.com/golang/glog"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/opencontainers/runc/libcontainer/devices"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/opencontainers/runtime-tools/generate"
	"golang.org/x/net/context"
	"k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1/runtime"

	containerstore "github.com/kubernetes-incubator/cri-containerd/pkg/store/container"
)

// CreateContainer creates a new container in the given PodSandbox.
func (c *criContainerdService) CreateContainer(ctx context.Context, r *runtime.CreateContainerRequest) (retRes *runtime.CreateContainerResponse, retErr error) {
	glog.V(2).Infof("CreateContainer within sandbox %q with container config %+v and sandbox config %+v",
		r.GetPodSandboxId(), r.GetConfig(), r.GetSandboxConfig())
	defer func() {
		if retErr == nil {
			glog.V(2).Infof("CreateContainer returns container id %q", retRes.GetContainerId())
		}
	}()

	config := r.GetConfig()
	sandboxConfig := r.GetSandboxConfig()
	sandbox, err := c.sandboxStore.Get(r.GetPodSandboxId())
	if err != nil {
		return nil, fmt.Errorf("failed to find sandbox id %q: %v", r.GetPodSandboxId(), err)
	}
	sandboxID := sandbox.ID

	// Generate unique id and name for the container and reserve the name.
	// Reserve the container name to avoid concurrent `CreateContainer` request creating
	// the same container.
	id := generateID()
	name := makeContainerName(config.GetMetadata(), sandboxConfig.GetMetadata())
	if err = c.containerNameIndex.Reserve(name, id); err != nil {
		return nil, fmt.Errorf("failed to reserve container name %q: %v", name, err)
	}
	defer func() {
		// Release the name if the function returns with an error.
		if retErr != nil {
			c.containerNameIndex.ReleaseByName(name)
		}
	}()

	// Create initial internal container metadata.
	meta := containerstore.Metadata{
		ID:        id,
		Name:      name,
		SandboxID: sandboxID,
		Config:    config,
	}

	// Prepare container image snapshot. For container, the image should have
	// been pulled before creating the container, so do not ensure the image.
	imageRef := config.GetImage().GetImage()
	image, err := c.localResolve(ctx, imageRef)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve image %q: %v", imageRef, err)
	}
	if image == nil {
		return nil, fmt.Errorf("image %q not found", imageRef)
	}

	// Generate container runtime spec.
	mounts := c.generateContainerMounts(getSandboxRootDir(c.rootDir, sandboxID), config)
	spec, err := c.generateContainerSpec(id, sandbox.Pid, config, sandboxConfig, image.Config, mounts)
	if err != nil {
		return nil, fmt.Errorf("failed to generate container %q spec: %v", id, err)
	}
	glog.V(4).Infof("Container spec: %+v", spec)

	var opts []containerd.NewContainerOpts
	// Prepare container rootfs.
	if config.GetLinux().GetSecurityContext().GetReadonlyRootfs() {
		opts = append(opts, containerd.WithNewSnapshotView(id, image.Image))
	} else {
		opts = append(opts, containerd.WithNewSnapshot(id, image.Image))
	}
	meta.ImageRef = image.ID

	// Create container root directory.
	containerRootDir := getContainerRootDir(c.rootDir, id)
	if err = c.os.MkdirAll(containerRootDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create container root directory %q: %v",
			containerRootDir, err)
	}
	defer func() {
		if retErr != nil {
			// Cleanup the container root directory.
			if err = c.os.RemoveAll(containerRootDir); err != nil {
				glog.Errorf("Failed to remove container root directory %q: %v",
					containerRootDir, err)
			}
		}
	}()

	opts = append(opts, containerd.WithSpec(spec), containerd.WithRuntime(defaultRuntime))
	var cntr containerd.Container
	if cntr, err = c.client.NewContainer(ctx, id, opts...); err != nil {
		return nil, fmt.Errorf("failed to create containerd container: %v", err)
	}
	defer func() {
		if retErr != nil {
			if err := cntr.Delete(ctx, containerd.WithSnapshotCleanup); err != nil {
				glog.Errorf("Failed to delete containerd container %q: %v", id, err)
			}
		}
	}()

	container, err := containerstore.NewContainer(meta,
		containerstore.Status{CreatedAt: time.Now().UnixNano()},
		containerstore.WithContainer(cntr))
	if err != nil {
		return nil, fmt.Errorf("failed to create internal container object for %q: %v",
			id, err)
	}
	defer func() {
		if retErr != nil {
			// Cleanup container checkpoint on error.
			if err := container.Delete(); err != nil {
				glog.Errorf("Failed to cleanup container checkpoint for %q: %v", id, err)
			}
		}
	}()

	// Add container into container store.
	if err := c.containerStore.Add(container); err != nil {
		return nil, fmt.Errorf("failed to add container %q into store: %v", id, err)
	}

	return &runtime.CreateContainerResponse{ContainerId: id}, nil
}

func (c *criContainerdService) generateContainerSpec(id string, sandboxPid uint32, config *runtime.ContainerConfig,
	sandboxConfig *runtime.PodSandboxConfig, imageConfig *imagespec.ImageConfig, extraMounts []*runtime.Mount) (*runtimespec.Spec, error) {
	// Creates a spec Generator with the default spec.
	spec, err := containerd.GenerateSpec()
	if err != nil {
		return nil, err
	}
	g := generate.NewFromSpec(spec)

	// Set the relative path to the rootfs of the container from containerd's
	// pre-defined directory.
	g.SetRootPath(relativeRootfsPath)

	if err := setOCIProcessArgs(&g, config, imageConfig); err != nil {
		return nil, err
	}

	if config.GetWorkingDir() != "" {
		g.SetProcessCwd(config.GetWorkingDir())
	} else if imageConfig.WorkingDir != "" {
		g.SetProcessCwd(imageConfig.WorkingDir)
	}

	g.SetProcessTerminal(config.GetTty())
	if config.GetTty() {
		g.AddProcessEnv("TERM", "xterm")
	}

	// Apply envs from image config first, so that envs from container config
	// can override them.
	if err := addImageEnvs(&g, imageConfig.Env); err != nil {
		return nil, err
	}
	for _, e := range config.GetEnvs() {
		g.AddProcessEnv(e.GetKey(), e.GetValue())
	}

	// TODO: add setOCIPrivileged group all privileged logic together
	securityContext := config.GetLinux().GetSecurityContext()

	// Add extra mounts first so that CRI specified mounts can override.
	addOCIBindMounts(&g, append(extraMounts, config.GetMounts()...), securityContext.GetPrivileged())

	g.SetRootReadonly(securityContext.GetReadonlyRootfs())

	if err := addOCIDevices(&g, config.GetDevices(), securityContext.GetPrivileged()); err != nil {
		return nil, fmt.Errorf("failed to set devices mapping %+v: %v", config.GetDevices(), err)
	}

	setOCILinuxResource(&g, config.GetLinux().GetResources())

	if sandboxConfig.GetLinux().GetCgroupParent() != "" {
		cgroupsPath := getCgroupsPath(sandboxConfig.GetLinux().GetCgroupParent(), id)
		g.SetLinuxCgroupsPath(cgroupsPath)
	}

	if err := setOCICapabilities(&g, securityContext.GetCapabilities(), securityContext.GetPrivileged()); err != nil {
		return nil, fmt.Errorf("failed to set capabilities %+v: %v",
			securityContext.GetCapabilities(), err)
	}

	// Set namespaces, share namespace with sandbox container.
	setOCINamespaces(&g, securityContext.GetNamespaceOptions(), sandboxPid)

	// TODO(random-liu): [P1] Set selinux options.

	// TODO(random-liu): [P1] Set user/username.

	supplementalGroups := securityContext.GetSupplementalGroups()
	for _, group := range supplementalGroups {
		g.AddProcessAdditionalGid(uint32(group))
	}

	// TODO(random-liu): [P2] Add apparmor and seccomp.

	return g.Spec(), nil
}

// generateContainerMounts sets up necessary container mounts including /dev/shm, /etc/hosts
// and /etc/resolv.conf.
func (c *criContainerdService) generateContainerMounts(sandboxRootDir string, config *runtime.ContainerConfig) []*runtime.Mount {
	var mounts []*runtime.Mount
	securityContext := config.GetLinux().GetSecurityContext()
	mounts = append(mounts, &runtime.Mount{
		ContainerPath: etcHosts,
		HostPath:      getSandboxHosts(sandboxRootDir),
		Readonly:      securityContext.GetReadonlyRootfs(),
	})

	// Mount sandbox resolv.config.
	// TODO: Need to figure out whether we should always mount it as read-only
	mounts = append(mounts, &runtime.Mount{
		ContainerPath: resolvConfPath,
		HostPath:      getResolvPath(sandboxRootDir),
		Readonly:      securityContext.GetReadonlyRootfs(),
	})

	sandboxDevShm := getSandboxDevShm(sandboxRootDir)
	if securityContext.GetNamespaceOptions().GetHostIpc() {
		sandboxDevShm = devShm
	}
	mounts = append(mounts, &runtime.Mount{
		ContainerPath: devShm,
		HostPath:      sandboxDevShm,
		Readonly:      false,
	})
	return mounts
}

// setOCIProcessArgs sets process args. It returns error if the final arg list
// is empty.
func setOCIProcessArgs(g *generate.Generator, config *runtime.ContainerConfig, imageConfig *imagespec.ImageConfig) error {
	command, args := config.GetCommand(), config.GetArgs()
	// The following logic is migrated from https://github.com/moby/moby/blob/master/daemon/commit.go
	// TODO(random-liu): Clearly define the commands overwrite behavior.
	if len(command) == 0 {
		if len(args) == 0 {
			args = imageConfig.Cmd
		}
		if command == nil {
			command = imageConfig.Entrypoint
		}
	}
	if len(command) == 0 && len(args) == 0 {
		return fmt.Errorf("no command specified")
	}
	g.SetProcessArgs(append(command, args...))
	return nil
}

// addImageEnvs adds environment variables from image config. It returns error if
// an invalid environment variable is encountered.
func addImageEnvs(g *generate.Generator, imageEnvs []string) error {
	for _, e := range imageEnvs {
		kv := strings.Split(e, "=")
		if len(kv) != 2 {
			return fmt.Errorf("invalid environment variable %q", e)
		}
		g.AddProcessEnv(kv[0], kv[1])
	}
	return nil
}

func clearReadOnly(m *runtimespec.Mount) {
	var opt []string
	for _, o := range m.Options {
		if o != "ro" {
			opt = append(opt, o)
		}
	}
	m.Options = opt
}

// addDevices set device mapping.
func addOCIDevices(g *generate.Generator, devs []*runtime.Device, privileged bool) error {
	spec := g.Spec()
	if privileged {
		hostDevices, err := devices.HostDevices()
		if err != nil {
			return err
		}
		for _, hostDevice := range hostDevices {
			rd := runtimespec.LinuxDevice{
				Path:  hostDevice.Path,
				Type:  string(hostDevice.Type),
				Major: hostDevice.Major,
				Minor: hostDevice.Minor,
				UID:   &hostDevice.Uid,
				GID:   &hostDevice.Gid,
			}
			if hostDevice.Major == 0 && hostDevice.Minor == 0 {
				// Invalid device, most likely a symbolic link, skip it.
				continue
			}
			g.AddDevice(rd)
		}
		spec.Linux.Resources.Devices = []runtimespec.LinuxDeviceCgroup{
			{
				Allow:  true,
				Access: "rwm",
			},
		}
		return nil
	}
	for _, device := range devs {
		path, err := resolveSymbolicLink(device.HostPath)
		if err != nil {
			return err
		}
		dev, err := devices.DeviceFromPath(path, device.Permissions)
		if err != nil {
			return err
		}
		rd := runtimespec.LinuxDevice{
			Path:  device.ContainerPath,
			Type:  string(dev.Type),
			Major: dev.Major,
			Minor: dev.Minor,
			UID:   &dev.Uid,
			GID:   &dev.Gid,
		}
		g.AddDevice(rd)
		spec.Linux.Resources.Devices = append(spec.Linux.Resources.Devices, runtimespec.LinuxDeviceCgroup{
			Allow:  true,
			Type:   string(dev.Type),
			Major:  &dev.Major,
			Minor:  &dev.Minor,
			Access: dev.Permissions,
		})
	}
	return nil
}

// addOCIBindMounts adds bind mounts.
// TODO(random-liu): Figure out whether we need to change all CRI mounts to readonly when
// rootfs is readonly. (https://github.com/moby/moby/blob/master/daemon/oci_linux.go)
func addOCIBindMounts(g *generate.Generator, mounts []*runtime.Mount, privileged bool) {
	// Mount cgroup into the container as readonly, which inherits docker's behavior.
	g.AddCgroupsMount("ro") // nolint: errcheck
	for _, mount := range mounts {
		dst := mount.GetContainerPath()
		src := mount.GetHostPath()
		options := []string{"rw"}
		if mount.GetReadonly() {
			options = []string{"ro"}
		}
		// TODO(random-liu): [P1] Apply selinux label
		g.AddBindMount(src, dst, options)
	}
	if !privileged {
		return
	}
	spec := g.Spec()
	// clear readonly for /sys and cgroup
	for i, m := range spec.Mounts {
		if spec.Mounts[i].Destination == "/sys" && !spec.Root.Readonly {
			clearReadOnly(&spec.Mounts[i])
		}
		if m.Type == "cgroup" {
			clearReadOnly(&spec.Mounts[i])
		}
	}
	spec.Linux.ReadonlyPaths = nil
	spec.Linux.MaskedPaths = nil
}

// setOCILinuxResource set container resource limit.
func setOCILinuxResource(g *generate.Generator, resources *runtime.LinuxContainerResources) {
	if resources == nil {
		return
	}
	g.SetLinuxResourcesCPUPeriod(uint64(resources.GetCpuPeriod()))
	g.SetLinuxResourcesCPUQuota(resources.GetCpuQuota())
	g.SetLinuxResourcesCPUShares(uint64(resources.GetCpuShares()))
	g.SetLinuxResourcesMemoryLimit(resources.GetMemoryLimitInBytes())
	g.SetProcessOOMScoreAdj(int(resources.GetOomScoreAdj()))
}

// setOCICapabilities adds/drops process capabilities.
func setOCICapabilities(g *generate.Generator, capabilities *runtime.Capability, privileged bool) error {
	if privileged {
		// Add all capabilities in privileged mode.
		g.SetupPrivileged(true)
		return nil
	}
	if capabilities == nil {
		return nil
	}

	// Capabilities in CRI doesn't have `CAP_` prefix, so add it.
	for _, c := range capabilities.GetAddCapabilities() {
		if err := g.AddProcessCapability("CAP_" + c); err != nil {
			return err
		}
	}

	for _, c := range capabilities.GetDropCapabilities() {
		if err := g.DropProcessCapability("CAP_" + c); err != nil {
			return err
		}
	}
	return nil
}

// setOCINamespaces sets namespaces.
func setOCINamespaces(g *generate.Generator, namespaces *runtime.NamespaceOption, sandboxPid uint32) {
	g.AddOrReplaceLinuxNamespace(string(runtimespec.NetworkNamespace), getNetworkNamespace(sandboxPid)) // nolint: errcheck
	g.AddOrReplaceLinuxNamespace(string(runtimespec.IPCNamespace), getIPCNamespace(sandboxPid))         // nolint: errcheck
	g.AddOrReplaceLinuxNamespace(string(runtimespec.UTSNamespace), getUTSNamespace(sandboxPid))         // nolint: errcheck
	g.AddOrReplaceLinuxNamespace(string(runtimespec.PIDNamespace), getPIDNamespace(sandboxPid))         // nolint: errcheck
}
