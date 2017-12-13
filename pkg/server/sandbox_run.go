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
	"os"
	"strings"

	"github.com/containerd/containerd"
	containerdio "github.com/containerd/containerd/cio"
	"github.com/containerd/containerd/linux/runctypes"
	"github.com/containerd/containerd/oci"
	"github.com/containerd/typeurl"
	"github.com/cri-o/ocicni/pkg/ocicni"
	"github.com/golang/glog"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	"golang.org/x/net/context"
	"golang.org/x/sys/unix"
	"k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1/runtime"

	"github.com/containerd/cri-containerd/pkg/annotations"
	customopts "github.com/containerd/cri-containerd/pkg/containerd/opts"
	sandboxstore "github.com/containerd/cri-containerd/pkg/store/sandbox"
	"github.com/containerd/cri-containerd/pkg/util"
)

func init() {
	typeurl.Register(&sandboxstore.Metadata{},
		"github.com/containerd/cri-containerd/pkg/store/sandbox", "Metadata")
}

// RunPodSandbox creates and starts a pod-level sandbox. Runtimes should ensure
// the sandbox is in ready state.
func (c *criContainerdService) RunPodSandbox(ctx context.Context, r *runtime.RunPodSandboxRequest) (_ *runtime.RunPodSandboxResponse, retErr error) {
	config := r.GetConfig()

	// Generate unique id and name for the sandbox and reserve the name.
	id := util.GenerateID()
	name := makeSandboxName(config.GetMetadata())
	glog.V(4).Infof("Generated id %q for sandbox %q", id, name)
	// Reserve the sandbox name to avoid concurrent `RunPodSandbox` request starting the
	// same sandbox.
	if err := c.sandboxNameIndex.Reserve(name, id); err != nil {
		return nil, fmt.Errorf("failed to reserve sandbox name %q: %v", name, err)
	}
	defer func() {
		// Release the name if the function returns with an error.
		if retErr != nil {
			c.sandboxNameIndex.ReleaseByName(name)
		}
	}()

	// Create initial internal sandbox object.
	sandbox := sandboxstore.Sandbox{
		Metadata: sandboxstore.Metadata{
			ID:     id,
			Name:   name,
			Config: config,
		},
	}

	// Ensure sandbox container image snapshot.
	image, err := c.ensureImageExists(ctx, c.config.SandboxImage)
	if err != nil {
		return nil, fmt.Errorf("failed to get sandbox image %q: %v", c.config.SandboxImage, err)
	}
	securityContext := config.GetLinux().GetSecurityContext()
	//Create Network Namespace if it is not in host network
	hostNet := securityContext.GetNamespaceOptions().GetHostNetwork()
	if !hostNet {
		// If it is not in host network namespace then create a namespace and set the sandbox
		// handle. NetNSPath in sandbox metadata and NetNS is non empty only for non host network
		// namespaces. If the pod is in host network namespace then both are empty and should not
		// be used.
		sandbox.NetNS, err = sandboxstore.NewNetNS()
		if err != nil {
			return nil, fmt.Errorf("failed to create network namespace for sandbox %q: %v", id, err)
		}
		sandbox.NetNSPath = sandbox.NetNS.GetPath()
		defer func() {
			if retErr != nil {
				if err := sandbox.NetNS.Remove(); err != nil {
					glog.Errorf("Failed to remove network namespace %s for sandbox %q: %v", sandbox.NetNSPath, id, err)
				}
				sandbox.NetNSPath = ""
			}
		}()
		// Setup network for sandbox.
		podNetwork := ocicni.PodNetwork{
			Name:         config.GetMetadata().GetName(),
			Namespace:    config.GetMetadata().GetNamespace(),
			ID:           id,
			NetNS:        sandbox.NetNSPath,
			PortMappings: toCNIPortMappings(config.GetPortMappings()),
		}
		if err = c.netPlugin.SetUpPod(podNetwork); err != nil {
			return nil, fmt.Errorf("failed to setup network for sandbox %q: %v", id, err)
		}
		defer func() {
			if retErr != nil {
				// Teardown network if an error is returned.
				if err := c.netPlugin.TearDownPod(podNetwork); err != nil {
					glog.Errorf("Failed to destroy network for sandbox %q: %v", id, err)
				}
			}
		}()
		ip, err := c.netPlugin.GetPodNetworkStatus(podNetwork)
		if err != nil {
			return nil, fmt.Errorf("failed to get network status for sandbox %q: %v", id, err)
		}
		// Certain VM based solutions like clear containers (Issue containerd/cri-containerd#524)
		//  rely on the assumption that CRI shim will not be querying the network namespace to check the
		// network states such as IP.
		// In furture runtime implementation should avoid relying on CRI shim implementation details.
		// In this case however caching the IP will add a subtle performance enhancement by avoiding
		// calls to network namespace of the pod to query the IP of the veth interface on every
		// SandboxStatus request.
		sandbox.IP = ip
	}

	// Create sandbox container.
	spec, err := c.generateSandboxContainerSpec(id, config, &image.ImageSpec.Config, sandbox.NetNSPath)
	if err != nil {
		return nil, fmt.Errorf("failed to generate sandbox container spec: %v", err)
	}
	glog.V(4).Infof("Sandbox container spec: %+v", spec)

	var specOpts []oci.SpecOpts
	if uid := securityContext.GetRunAsUser(); uid != nil {
		specOpts = append(specOpts, oci.WithUserID(uint32(uid.GetValue())))
	}

	seccompSpecOpts, err := generateSeccompSpecOpts(
		securityContext.GetSeccompProfilePath(),
		securityContext.GetPrivileged(),
		c.seccompEnabled)
	if err != nil {
		return nil, fmt.Errorf("failed to generate seccomp spec opts: %v", err)
	}
	if seccompSpecOpts != nil {
		specOpts = append(specOpts, seccompSpecOpts)
	}

	sandboxLabels := buildLabels(config.Labels, containerKindSandbox)

	opts := []containerd.NewContainerOpts{
		containerd.WithSnapshotter(c.config.ContainerdConfig.Snapshotter),
		customopts.WithNewSnapshot(id, image.Image),
		containerd.WithSpec(spec, specOpts...),
		containerd.WithContainerLabels(sandboxLabels),
		containerd.WithContainerExtension(sandboxMetadataExtension, &sandbox.Metadata),
		containerd.WithRuntime(
			c.config.ContainerdConfig.Runtime,
			&runctypes.RuncOptions{
				Runtime:       c.config.ContainerdConfig.RuntimeEngine,
				RuntimeRoot:   c.config.ContainerdConfig.RuntimeRoot,
				SystemdCgroup: c.config.SystemdCgroup})} // TODO (mikebrow): add CriuPath when we add support for pause

	container, err := c.client.NewContainer(ctx, id, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create containerd container: %v", err)
	}
	defer func() {
		if retErr != nil {
			if err := container.Delete(ctx, containerd.WithSnapshotCleanup); err != nil {
				glog.Errorf("Failed to delete containerd container %q: %v", id, err)
			}
		}
	}()

	// Create sandbox container root directory.
	sandboxRootDir := getSandboxRootDir(c.config.RootDir, id)
	if err := c.os.MkdirAll(sandboxRootDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create sandbox root directory %q: %v",
			sandboxRootDir, err)
	}
	defer func() {
		if retErr != nil {
			// Cleanup the sandbox root directory.
			if err := c.os.RemoveAll(sandboxRootDir); err != nil {
				glog.Errorf("Failed to remove sandbox root directory %q: %v",
					sandboxRootDir, err)
			}
		}
	}()

	// Setup sandbox /dev/shm, /etc/hosts and /etc/resolv.conf.
	if err = c.setupSandboxFiles(sandboxRootDir, config); err != nil {
		return nil, fmt.Errorf("failed to setup sandbox files: %v", err)
	}
	defer func() {
		if retErr != nil {
			if err = c.unmountSandboxFiles(sandboxRootDir, config); err != nil {
				glog.Errorf("Failed to unmount sandbox files in %q: %v",
					sandboxRootDir, err)
			}
		}
	}()

	// Create sandbox task in containerd.
	glog.V(5).Infof("Create sandbox container (id=%q, name=%q).",
		id, name)
	// We don't need stdio for sandbox container.
	task, err := container.NewTask(ctx, containerdio.NullIO)
	if err != nil {
		return nil, fmt.Errorf("failed to create containerd task: %v", err)
	}
	defer func() {
		if retErr != nil {
			// Cleanup the sandbox container if an error is returned.
			if _, err := task.Delete(ctx, containerd.WithProcessKill); err != nil {
				glog.Errorf("Failed to delete sandbox container %q: %v", id, err)
			}
		}
	}()

	if err = task.Start(ctx); err != nil {
		return nil, fmt.Errorf("failed to start sandbox container task %q: %v",
			id, err)
	}

	// Add sandbox into sandbox store.
	sandbox.Container = container
	if err := c.sandboxStore.Add(sandbox); err != nil {
		return nil, fmt.Errorf("failed to add sandbox %+v into store: %v", sandbox, err)
	}

	return &runtime.RunPodSandboxResponse{PodSandboxId: id}, nil
}

func (c *criContainerdService) generateSandboxContainerSpec(id string, config *runtime.PodSandboxConfig,
	imageConfig *imagespec.ImageConfig, nsPath string) (*runtimespec.Spec, error) {
	// Creates a spec Generator with the default spec.
	// TODO(random-liu): [P1] Compare the default settings with docker and containerd default.
	spec, err := defaultRuntimeSpec(id)
	if err != nil {
		return nil, err
	}
	g := newSpecGenerator(spec)

	// Apply default config from image config.
	if err := addImageEnvs(&g, imageConfig.Env); err != nil {
		return nil, err
	}

	if imageConfig.WorkingDir != "" {
		g.SetProcessCwd(imageConfig.WorkingDir)
	}

	if len(imageConfig.Entrypoint) == 0 {
		// Pause image must have entrypoint.
		return nil, fmt.Errorf("invalid empty entrypoint in image config %+v", imageConfig)
	}
	// Set process commands.
	g.SetProcessArgs(append(imageConfig.Entrypoint, imageConfig.Cmd...))

	// Set relative root path.
	g.SetRootPath(relativeRootfsPath)

	// Make root of sandbox container read-only.
	g.SetRootReadonly(true)

	// Set hostname.
	g.SetHostname(config.GetHostname())

	// TODO(random-liu): [P2] Consider whether to add labels and annotations to the container.

	// Set cgroups parent.
	if config.GetLinux().GetCgroupParent() != "" {
		cgroupsPath := getCgroupsPath(config.GetLinux().GetCgroupParent(), id,
			c.config.SystemdCgroup)
		g.SetLinuxCgroupsPath(cgroupsPath)
	}
	// When cgroup parent is not set, containerd-shim will create container in a child cgroup
	// of the cgroup itself is in.
	// TODO(random-liu): [P2] Set default cgroup path if cgroup parent is not specified.

	// Set namespace options.
	securityContext := config.GetLinux().GetSecurityContext()
	nsOptions := securityContext.GetNamespaceOptions()
	if nsOptions.GetHostNetwork() {
		g.RemoveLinuxNamespace(string(runtimespec.NetworkNamespace)) // nolint: errcheck
	} else {
		//TODO(Abhi): May be move this to containerd spec opts (WithLinuxSpaceOption)
		g.AddOrReplaceLinuxNamespace(string(runtimespec.NetworkNamespace), nsPath) // nolint: errcheck
	}
	if nsOptions.GetHostPid() {
		g.RemoveLinuxNamespace(string(runtimespec.PIDNamespace)) // nolint: errcheck
	}

	if nsOptions.GetHostIpc() {
		g.RemoveLinuxNamespace(string(runtimespec.IPCNamespace)) // nolint: errcheck
	}

	selinuxOpt := securityContext.GetSelinuxOptions()
	processLabel, mountLabel, err := initSelinuxOpts(selinuxOpt)
	if err != nil {
		return nil, fmt.Errorf("failed to init selinux options %+v: %v", securityContext.GetSelinuxOptions(), err)
	}
	g.SetProcessSelinuxLabel(processLabel)
	g.SetLinuxMountLabel(mountLabel)

	supplementalGroups := securityContext.GetSupplementalGroups()
	for _, group := range supplementalGroups {
		g.AddProcessAdditionalGid(uint32(group))
	}

	// Add sysctls
	sysctls := config.GetLinux().GetSysctls()
	for key, value := range sysctls {
		g.AddLinuxSysctl(key, value)
	}

	// Note: LinuxSandboxSecurityContext does not currently provide an apparmor profile

	g.SetLinuxResourcesCPUShares(uint64(defaultSandboxCPUshares))
	g.SetProcessOOMScoreAdj(int(defaultSandboxOOMAdj))

	g.AddAnnotation(annotations.ContainerType, annotations.ContainerTypeSandbox)
	g.AddAnnotation(annotations.SandboxID, id)

	return g.Spec(), nil
}

// setupSandboxFiles sets up necessary sandbox files including /dev/shm, /etc/hosts
// and /etc/resolv.conf.
func (c *criContainerdService) setupSandboxFiles(rootDir string, config *runtime.PodSandboxConfig) error {
	// TODO(random-liu): Consider whether we should maintain /etc/hosts and /etc/resolv.conf in kubelet.
	sandboxEtcHosts := getSandboxHosts(rootDir)
	if err := c.os.CopyFile(etcHosts, sandboxEtcHosts, 0644); err != nil {
		return fmt.Errorf("failed to generate sandbox hosts file %q: %v", sandboxEtcHosts, err)
	}

	// Set DNS options. Maintain a resolv.conf for the sandbox.
	var err error
	resolvContent := ""
	if dnsConfig := config.GetDnsConfig(); dnsConfig != nil {
		resolvContent, err = parseDNSOptions(dnsConfig.Servers, dnsConfig.Searches, dnsConfig.Options)
		if err != nil {
			return fmt.Errorf("failed to parse sandbox DNSConfig %+v: %v", dnsConfig, err)
		}
	}
	resolvPath := getResolvPath(rootDir)
	if resolvContent == "" {
		// copy host's resolv.conf to resolvPath
		err = c.os.CopyFile(resolvConfPath, resolvPath, 0644)
		if err != nil {
			return fmt.Errorf("failed to copy host's resolv.conf to %q: %v", resolvPath, err)
		}
	} else {
		err = c.os.WriteFile(resolvPath, []byte(resolvContent), 0644)
		if err != nil {
			return fmt.Errorf("failed to write resolv content to %q: %v", resolvPath, err)
		}
	}

	// Setup sandbox /dev/shm.
	if config.GetLinux().GetSecurityContext().GetNamespaceOptions().GetHostIpc() {
		if _, err := c.os.Stat(devShm); err != nil {
			return fmt.Errorf("host %q is not available for host ipc: %v", devShm, err)
		}
	} else {
		sandboxDevShm := getSandboxDevShm(rootDir)
		if err := c.os.MkdirAll(sandboxDevShm, 0700); err != nil {
			return fmt.Errorf("failed to create sandbox shm: %v", err)
		}
		shmproperty := fmt.Sprintf("mode=1777,size=%d", defaultShmSize)
		if err := c.os.Mount("shm", sandboxDevShm, "tmpfs", uintptr(unix.MS_NOEXEC|unix.MS_NOSUID|unix.MS_NODEV), shmproperty); err != nil {
			return fmt.Errorf("failed to mount sandbox shm: %v", err)
		}
	}

	return nil
}

// parseDNSOptions parse DNS options into resolv.conf format content,
// if none option is specified, will return empty with no error.
func parseDNSOptions(servers, searches, options []string) (string, error) {
	resolvContent := ""

	if len(searches) > maxDNSSearches {
		return "", fmt.Errorf("DNSOption.Searches has more than 6 domains")
	}

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

// unmountSandboxFiles unmount some sandbox files, we rely on the removal of sandbox root directory to
// remove these files. Unmount should *NOT* return error when:
//  1) The mount point is already unmounted.
//  2) The mount point doesn't exist.
func (c *criContainerdService) unmountSandboxFiles(rootDir string, config *runtime.PodSandboxConfig) error {
	if !config.GetLinux().GetSecurityContext().GetNamespaceOptions().GetHostIpc() {
		if err := c.os.Unmount(getSandboxDevShm(rootDir), unix.MNT_DETACH); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// toCNIPortMappings converts CRI port mappings to CNI.
func toCNIPortMappings(criPortMappings []*runtime.PortMapping) []ocicni.PortMapping {
	var portMappings []ocicni.PortMapping
	for _, mapping := range criPortMappings {
		if mapping.HostPort <= 0 {
			continue
		}
		portMappings = append(portMappings, ocicni.PortMapping{
			HostPort:      mapping.HostPort,
			ContainerPort: mapping.ContainerPort,
			Protocol:      strings.ToLower(mapping.Protocol.String()),
			HostIP:        mapping.HostIp,
		})
	}
	return portMappings
}
