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
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/linux/runctypes"
	"github.com/containerd/containerd/oci"
	cni "github.com/containerd/go-cni"
	"github.com/containerd/typeurl"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"golang.org/x/net/context"
	"golang.org/x/sys/unix"
	runtime "k8s.io/kubernetes/pkg/kubelet/apis/cri/runtime/v1alpha2"

	"github.com/containerd/cri/pkg/annotations"
	criconfig "github.com/containerd/cri/pkg/config"
	customopts "github.com/containerd/cri/pkg/containerd/opts"
	ctrdutil "github.com/containerd/cri/pkg/containerd/util"
	"github.com/containerd/cri/pkg/log"
	sandboxstore "github.com/containerd/cri/pkg/store/sandbox"
	"github.com/containerd/cri/pkg/util"
)

func init() {
	typeurl.Register(&sandboxstore.Metadata{},
		"github.com/containerd/cri/pkg/store/sandbox", "Metadata")
}

// RunPodSandbox creates and starts a pod-level sandbox. Runtimes should ensure
// the sandbox is in ready state.
func (c *criService) RunPodSandbox(ctx context.Context, r *runtime.RunPodSandboxRequest) (_ *runtime.RunPodSandboxResponse, retErr error) {
	config := r.GetConfig()

	// Generate unique id and name for the sandbox and reserve the name.
	id := util.GenerateID()
	name := makeSandboxName(config.GetMetadata())
	logrus.Debugf("Generated id %q for sandbox %q", id, name)
	// Reserve the sandbox name to avoid concurrent `RunPodSandbox` request starting the
	// same sandbox.
	if err := c.sandboxNameIndex.Reserve(name, id); err != nil {
		return nil, errors.Wrapf(err, "failed to reserve sandbox name %q", name)
	}
	defer func() {
		// Release the name if the function returns with an error.
		if retErr != nil {
			c.sandboxNameIndex.ReleaseByName(name)
		}
	}()

	// Create initial internal sandbox object.
	sandbox := sandboxstore.NewSandbox(
		sandboxstore.Metadata{
			ID:     id,
			Name:   name,
			Config: config,
		},
		sandboxstore.Status{
			State: sandboxstore.StateUnknown,
		},
	)

	// Ensure sandbox container image snapshot.
	image, err := c.ensureImageExists(ctx, c.config.SandboxImage)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get sandbox image %q", c.config.SandboxImage)
	}
	securityContext := config.GetLinux().GetSecurityContext()
	//Create Network Namespace if it is not in host network
	hostNet := securityContext.GetNamespaceOptions().GetNetwork() == runtime.NamespaceMode_NODE
	if !hostNet {
		// If it is not in host network namespace then create a namespace and set the sandbox
		// handle. NetNSPath in sandbox metadata and NetNS is non empty only for non host network
		// namespaces. If the pod is in host network namespace then both are empty and should not
		// be used.
		sandbox.NetNS, err = sandboxstore.NewNetNS()
		if err != nil {
			return nil, errors.Wrapf(err, "failed to create network namespace for sandbox %q", id)
		}
		sandbox.NetNSPath = sandbox.NetNS.GetPath()
		defer func() {
			if retErr != nil {
				if err := sandbox.NetNS.Remove(); err != nil {
					logrus.WithError(err).Errorf("Failed to remove network namespace %s for sandbox %q", sandbox.NetNSPath, id)
				}
				sandbox.NetNSPath = ""
			}
		}()
		// Setup network for sandbox.
		// Certain VM based solutions like clear containers (Issue containerd/cri-containerd#524)
		// rely on the assumption that CRI shim will not be querying the network namespace to check the
		// network states such as IP.
		// In future runtime implementation should avoid relying on CRI shim implementation details.
		// In this case however caching the IP will add a subtle performance enhancement by avoiding
		// calls to network namespace of the pod to query the IP of the veth interface on every
		// SandboxStatus request.
		sandbox.IP, err = c.setupPod(id, sandbox.NetNSPath, config)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to setup network for sandbox %q", id)
		}
		defer func() {
			if retErr != nil {
				// Teardown network if an error is returned.
				if err := c.teardownPod(id, sandbox.NetNSPath, config); err != nil {
					logrus.WithError(err).Errorf("Failed to destroy network for sandbox %q", id)
				}
			}
		}()
	}

	ociRuntime, err := c.getSandboxRuntime(config)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get sandbox runtime")
	}
	logrus.Debugf("Use OCI %+v for sandbox %q", ociRuntime, id)

	// Create sandbox container.
	spec, err := c.generateSandboxContainerSpec(id, config, &image.ImageSpec.Config, sandbox.NetNSPath)
	if err != nil {
		return nil, errors.Wrap(err, "failed to generate sandbox container spec")
	}
	logrus.Debugf("Sandbox container spec: %+v", spec)

	var specOpts []oci.SpecOpts
	if uid := securityContext.GetRunAsUser(); uid != nil {
		specOpts = append(specOpts, oci.WithUserID(uint32(uid.GetValue())))
	}

	seccompSpecOpts, err := generateSeccompSpecOpts(
		securityContext.GetSeccompProfilePath(),
		securityContext.GetPrivileged(),
		c.seccompEnabled)
	if err != nil {
		return nil, errors.Wrap(err, "failed to generate seccomp spec opts")
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
			ociRuntime.Type,
			&runctypes.RuncOptions{
				Runtime:       ociRuntime.Engine,
				RuntimeRoot:   ociRuntime.Root,
				SystemdCgroup: c.config.SystemdCgroup})} // TODO (mikebrow): add CriuPath when we add support for pause

	container, err := c.client.NewContainer(ctx, id, opts...)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create containerd container")
	}
	defer func() {
		if retErr != nil {
			deferCtx, deferCancel := ctrdutil.DeferContext()
			defer deferCancel()
			if err := container.Delete(deferCtx, containerd.WithSnapshotCleanup); err != nil {
				logrus.WithError(err).Errorf("Failed to delete containerd container %q", id)
			}
		}
	}()

	// Create sandbox container root directories.
	sandboxRootDir := c.getSandboxRootDir(id)
	if err := c.os.MkdirAll(sandboxRootDir, 0755); err != nil {
		return nil, errors.Wrapf(err, "failed to create sandbox root directory %q",
			sandboxRootDir)
	}
	defer func() {
		if retErr != nil {
			// Cleanup the sandbox root directory.
			if err := c.os.RemoveAll(sandboxRootDir); err != nil {
				logrus.WithError(err).Errorf("Failed to remove sandbox root directory %q",
					sandboxRootDir)
			}
		}
	}()
	volatileSandboxRootDir := c.getVolatileSandboxRootDir(id)
	if err := c.os.MkdirAll(volatileSandboxRootDir, 0755); err != nil {
		return nil, errors.Wrapf(err, "failed to create volatile sandbox root directory %q",
			volatileSandboxRootDir)
	}
	defer func() {
		if retErr != nil {
			// Cleanup the volatile sandbox root directory.
			if err := c.os.RemoveAll(volatileSandboxRootDir); err != nil {
				logrus.WithError(err).Errorf("Failed to remove volatile sandbox root directory %q",
					volatileSandboxRootDir)
			}
		}
	}()

	// Setup sandbox /dev/shm, /etc/hosts and /etc/resolv.conf.
	if err = c.setupSandboxFiles(id, config); err != nil {
		return nil, errors.Wrapf(err, "failed to setup sandbox files")
	}
	defer func() {
		if retErr != nil {
			if err = c.unmountSandboxFiles(id, config); err != nil {
				logrus.WithError(err).Errorf("Failed to unmount sandbox files in %q",
					sandboxRootDir)
			}
		}
	}()

	// Update sandbox created timestamp.
	info, err := container.Info(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get sandbox container info")
	}
	if err := sandbox.Status.Update(func(status sandboxstore.Status) (sandboxstore.Status, error) {
		status.CreatedAt = info.CreatedAt
		return status, nil
	}); err != nil {
		return nil, errors.Wrap(err, "failed to update sandbox created timestamp")
	}

	// Add sandbox into sandbox store in UNKNOWN state.
	sandbox.Container = container
	if err := c.sandboxStore.Add(sandbox); err != nil {
		return nil, errors.Wrapf(err, "failed to add sandbox %+v into store", sandbox)
	}
	defer func() {
		// Delete sandbox from sandbox store if there is an error.
		if retErr != nil {
			c.sandboxStore.Delete(id)
		}
	}()
	// NOTE(random-liu): Sandbox state only stay in UNKNOWN state after this point
	// and before the end of this function.
	// * If `Update` succeeds, sandbox state will become READY in one transaction.
	// * If `Update` fails, sandbox will be removed from the store in the defer above.
	// * If containerd stops at any point before `Update` finishes, because sandbox
	// state is not checkpointed, it will be recovered from corresponding containerd task
	// status during restart:
	//   * If the task is running, sandbox state will be READY,
	//   * Or else, sandbox state will be NOTREADY.
	//
	// In any case, sandbox will leave UNKNOWN state, so it's safe to ignore sandbox
	// in UNKNOWN state in other functions.

	// Start sandbox container in one transaction to avoid race condition with
	// event monitor.
	if err := sandbox.Status.Update(func(status sandboxstore.Status) (_ sandboxstore.Status, retErr error) {
		// NOTE(random-liu): We should not change the sandbox state to NOTREADY
		// if `Update` fails.
		//
		// If `Update` fails, the sandbox will be cleaned up by all the defers
		// above. We should not let user see this sandbox, or else they will
		// see the sandbox disappear after the defer clean up, which may confuse
		// them.
		//
		// Given so, we should keep the sandbox in UNKNOWN state if `Update` fails,
		// and ignore sandbox in UNKNOWN state in all the inspection functions.

		// Create sandbox task in containerd.
		log.Tracef("Create sandbox container (id=%q, name=%q).",
			id, name)
		// We don't need stdio for sandbox container.
		task, err := container.NewTask(ctx, containerdio.NullIO)
		if err != nil {
			return status, errors.Wrap(err, "failed to create containerd task")
		}
		defer func() {
			if retErr != nil {
				deferCtx, deferCancel := ctrdutil.DeferContext()
				defer deferCancel()
				// Cleanup the sandbox container if an error is returned.
				// It's possible that task is deleted by event monitor.
				if _, err := task.Delete(deferCtx, containerd.WithProcessKill); err != nil && !errdefs.IsNotFound(err) {
					logrus.WithError(err).Errorf("Failed to delete sandbox container %q", id)
				}
			}
		}()

		if err := task.Start(ctx); err != nil {
			return status, errors.Wrapf(err, "failed to start sandbox container task %q", id)
		}

		// Set the pod sandbox as ready after successfully start sandbox container.
		status.Pid = task.Pid()
		status.State = sandboxstore.StateReady
		return status, nil
	}); err != nil {
		return nil, errors.Wrap(err, "failed to start sandbox container")
	}

	return &runtime.RunPodSandboxResponse{PodSandboxId: id}, nil
}

func (c *criService) generateSandboxContainerSpec(id string, config *runtime.PodSandboxConfig,
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
		return nil, errors.Errorf("invalid empty entrypoint in image config %+v", imageConfig)
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
	if nsOptions.GetNetwork() == runtime.NamespaceMode_NODE {
		g.RemoveLinuxNamespace(string(runtimespec.NetworkNamespace)) // nolint: errcheck
	} else {
		//TODO(Abhi): May be move this to containerd spec opts (WithLinuxSpaceOption)
		g.AddOrReplaceLinuxNamespace(string(runtimespec.NetworkNamespace), nsPath) // nolint: errcheck
	}
	if nsOptions.GetPid() == runtime.NamespaceMode_NODE {
		g.RemoveLinuxNamespace(string(runtimespec.PIDNamespace)) // nolint: errcheck
	}
	if nsOptions.GetIpc() == runtime.NamespaceMode_NODE {
		g.RemoveLinuxNamespace(string(runtimespec.IPCNamespace)) // nolint: errcheck
	}

	selinuxOpt := securityContext.GetSelinuxOptions()
	processLabel, mountLabel, err := initSelinuxOpts(selinuxOpt)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to init selinux options %+v", securityContext.GetSelinuxOptions())
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
func (c *criService) setupSandboxFiles(id string, config *runtime.PodSandboxConfig) error {
	// TODO(random-liu): Consider whether we should maintain /etc/hosts and /etc/resolv.conf in kubelet.
	sandboxEtcHosts := c.getSandboxHosts(id)
	if err := c.os.CopyFile(etcHosts, sandboxEtcHosts, 0644); err != nil {
		return errors.Wrapf(err, "failed to generate sandbox hosts file %q", sandboxEtcHosts)
	}

	// Set DNS options. Maintain a resolv.conf for the sandbox.
	var err error
	resolvContent := ""
	if dnsConfig := config.GetDnsConfig(); dnsConfig != nil {
		resolvContent, err = parseDNSOptions(dnsConfig.Servers, dnsConfig.Searches, dnsConfig.Options)
		if err != nil {
			return errors.Wrapf(err, "failed to parse sandbox DNSConfig %+v", dnsConfig)
		}
	}
	resolvPath := c.getResolvPath(id)
	if resolvContent == "" {
		// copy host's resolv.conf to resolvPath
		err = c.os.CopyFile(resolvConfPath, resolvPath, 0644)
		if err != nil {
			return errors.Wrapf(err, "failed to copy host's resolv.conf to %q", resolvPath)
		}
	} else {
		err = c.os.WriteFile(resolvPath, []byte(resolvContent), 0644)
		if err != nil {
			return errors.Wrapf(err, "failed to write resolv content to %q", resolvPath)
		}
	}

	// Setup sandbox /dev/shm.
	if config.GetLinux().GetSecurityContext().GetNamespaceOptions().GetIpc() == runtime.NamespaceMode_NODE {
		if _, err := c.os.Stat(devShm); err != nil {
			return errors.Wrapf(err, "host %q is not available for host ipc", devShm)
		}
	} else {
		sandboxDevShm := c.getSandboxDevShm(id)
		if err := c.os.MkdirAll(sandboxDevShm, 0700); err != nil {
			return errors.Wrap(err, "failed to create sandbox shm")
		}
		shmproperty := fmt.Sprintf("mode=1777,size=%d", defaultShmSize)
		if err := c.os.Mount("shm", sandboxDevShm, "tmpfs", uintptr(unix.MS_NOEXEC|unix.MS_NOSUID|unix.MS_NODEV), shmproperty); err != nil {
			return errors.Wrap(err, "failed to mount sandbox shm")
		}
	}

	return nil
}

// parseDNSOptions parse DNS options into resolv.conf format content,
// if none option is specified, will return empty with no error.
func parseDNSOptions(servers, searches, options []string) (string, error) {
	resolvContent := ""

	if len(searches) > maxDNSSearches {
		return "", errors.New("DNSOption.Searches has more than 6 domains")
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
func (c *criService) unmountSandboxFiles(id string, config *runtime.PodSandboxConfig) error {
	if config.GetLinux().GetSecurityContext().GetNamespaceOptions().GetIpc() != runtime.NamespaceMode_NODE {
		path, err := c.os.FollowSymlinkInScope(c.getSandboxDevShm(id), "/")
		if err != nil {
			return errors.Wrap(err, "failed to follow symlink")
		}
		if err := c.os.Unmount(path, unix.MNT_DETACH); err != nil && !os.IsNotExist(err) {
			return errors.Wrapf(err, "failed to unmount %q", path)
		}
	}
	return nil
}

// setupPod setups up the network for a pod
func (c *criService) setupPod(id string, path string, config *runtime.PodSandboxConfig) (string, error) {
	if c.netPlugin == nil {
		return "", errors.New("cni config not intialized")
	}

	labels := getPodCNILabels(id, config)
	result, err := c.netPlugin.Setup(id,
		path,
		cni.WithLabels(labels),
		cni.WithCapabilityPortMap(toCNIPortMappings(config.GetPortMappings())))
	if err != nil {
		return "", err
	}
	// Check if the default interface has IP config
	if configs, ok := result.Interfaces[defaultIfName]; ok && len(configs.IPConfigs) > 0 {
		return configs.IPConfigs[0].IP.String(), nil
	}
	// If it comes here then the result was invalid so destroy the pod network and return error
	if err := c.teardownPod(id, path, config); err != nil {
		logrus.WithError(err).Errorf("Failed to destroy network for sandbox %q", id)
	}
	return "", errors.Errorf("failed to find network info for sandbox %q", id)
}

// toCNIPortMappings converts CRI port mappings to CNI.
func toCNIPortMappings(criPortMappings []*runtime.PortMapping) []cni.PortMapping {
	var portMappings []cni.PortMapping
	for _, mapping := range criPortMappings {
		if mapping.HostPort <= 0 {
			continue
		}
		portMappings = append(portMappings, cni.PortMapping{
			HostPort:      mapping.HostPort,
			ContainerPort: mapping.ContainerPort,
			Protocol:      strings.ToLower(mapping.Protocol.String()),
			HostIP:        mapping.HostIp,
		})
	}
	return portMappings
}

// untrustedWorkload returns true if the sandbox contains untrusted workload.
func untrustedWorkload(config *runtime.PodSandboxConfig) bool {
	return config.GetAnnotations()[annotations.UntrustedWorkload] == "true"
}

// hostPrivilegedSandbox returns true if the sandbox configuration
// requires additional host privileges for the sandbox.
func hostPrivilegedSandbox(config *runtime.PodSandboxConfig) bool {
	securityContext := config.GetLinux().GetSecurityContext()
	if securityContext.GetPrivileged() {
		return true
	}

	namespaceOptions := securityContext.GetNamespaceOptions()
	if namespaceOptions.GetNetwork() == runtime.NamespaceMode_NODE ||
		namespaceOptions.GetPid() == runtime.NamespaceMode_NODE ||
		namespaceOptions.GetIpc() == runtime.NamespaceMode_NODE {
		return true
	}

	return false
}

// getSandboxRuntime returns the runtime configuration for sandbox.
// If the sandbox contains untrusted workload, runtime for untrusted workload will be returned,
// or else default runtime will be returned.
func (c *criService) getSandboxRuntime(config *runtime.PodSandboxConfig) (criconfig.Runtime, error) {
	untrusted := false
	if untrustedWorkload(config) {
		// TODO(random-liu): Figure out we should return error or not.
		if hostPrivilegedSandbox(config) {
			return criconfig.Runtime{}, errors.New("untrusted workload with host privilege is not allowed")
		}
		untrusted = true
	}

	if untrusted {
		if c.config.ContainerdConfig.UntrustedWorkloadRuntime.Type == "" {
			return criconfig.Runtime{}, errors.New("no runtime for untrusted workload is configured")
		}
		return c.config.ContainerdConfig.UntrustedWorkloadRuntime, nil
	}
	return c.config.ContainerdConfig.DefaultRuntime, nil
}
