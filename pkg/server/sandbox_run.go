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
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/containerd/containerd"
	"github.com/golang/glog"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/opencontainers/runtime-tools/generate"
	"golang.org/x/net/context"
	"golang.org/x/sys/unix"
	"k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1/runtime"

	sandboxstore "github.com/kubernetes-incubator/cri-containerd/pkg/store/sandbox"
)

// RunPodSandbox creates and starts a pod-level sandbox. Runtimes should ensure
// the sandbox is in ready state.
func (c *criContainerdService) RunPodSandbox(ctx context.Context, r *runtime.RunPodSandboxRequest) (retRes *runtime.RunPodSandboxResponse, retErr error) {
	glog.V(2).Infof("RunPodSandbox with config %+v", r.GetConfig())
	defer func() {
		if retErr == nil {
			glog.V(2).Infof("RunPodSandbox returns sandbox id %q", retRes.GetPodSandboxId())
		}
	}()

	config := r.GetConfig()

	// Generate unique id and name for the sandbox and reserve the name.
	id := generateID()
	name := makeSandboxName(config.GetMetadata())
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
	image, err := c.ensureImageExists(ctx, c.sandboxImage)
	if err != nil {
		return nil, fmt.Errorf("failed to get sandbox image %q: %v", c.sandboxImage, err)
	}

	// Create sandbox container.
	spec, err := c.generateSandboxContainerSpec(id, config, image.Config)
	if err != nil {
		return nil, fmt.Errorf("failed to generate sandbox container spec: %v", err)
	}
	glog.V(4).Infof("Sandbox container spec: %+v", spec)
	// TODO(random-liu): Checkpoint metadata into container labels.

	opts := []containerd.NewContainerOpts{
		containerd.WithSpec(spec),
		containerd.WithRuntime(defaultRuntime),
		containerd.WithNewSnapshotView(id, image.Image)}
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
	sandboxRootDir := getSandboxRootDir(c.rootDir, id)
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

	// Discard sandbox container output because we don't care about it.
	rStdoutPipe, wStdoutPipe := io.Pipe()
	rStderrPipe, wStderrPipe := io.Pipe()
	defer func() {
		if retErr != nil {
			rStdoutPipe.Close()
			rStderrPipe.Close()
		}
	}()
	if err := c.agentFactory.NewSandboxLogger(rStdoutPipe).Start(); err != nil {
		return nil, fmt.Errorf("failed to start sandbox stdout logger: %v", err)
	}
	if err := c.agentFactory.NewSandboxLogger(rStderrPipe).Start(); err != nil {
		return nil, fmt.Errorf("failed to start sandbox stderr logger: %v", err)
	}

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
	//TODO(Abhi): close the stdin or pass newIOCreation with /dev/null stdin
	task, err := container.NewTask(ctx, containerd.NewIO(new(bytes.Buffer), wStdoutPipe, wStderrPipe))
	if err != nil {
		return nil, fmt.Errorf("failed to create task for sandbox %q: %v", id, err)
	}
	defer func() {
		if retErr != nil {
			// Cleanup the sandbox container if an error is returned.
			if _, err := task.Delete(ctx, containerd.WithProcessKill); err != nil {
				glog.Errorf("Failed to delete sandbox container %q: %v", id, err)
			}
		}
	}()

	sandbox.Pid = task.Pid()
	sandbox.NetNS = getNetworkNamespace(task.Pid())
	if !config.GetLinux().GetSecurityContext().GetNamespaceOptions().GetHostNetwork() {
		// Setup network for sandbox.
		// TODO(random-liu): [P2] Replace with permanent network namespace.
		podName := config.GetMetadata().GetName()
		if err = c.netPlugin.SetUpPod(sandbox.NetNS, config.GetMetadata().GetNamespace(), podName, id); err != nil {
			return nil, fmt.Errorf("failed to setup network for sandbox %q: %v", id, err)
		}
		defer func() {
			if retErr != nil {
				// Teardown network if an error is returned.
				if err := c.netPlugin.TearDownPod(sandbox.NetNS, config.GetMetadata().GetNamespace(), podName, id); err != nil {
					glog.Errorf("failed to destroy network for sandbox %q: %v", id, err)
				}
			}
		}()
	}

	if err = task.Start(ctx); err != nil {
		return nil, fmt.Errorf("failed to start sandbox container task %q: %v",
			id, err)
	}

	// Add sandbox into sandbox store.
	sandbox.CreatedAt = time.Now().UnixNano()
	sandbox.Container = container
	if err := c.sandboxStore.Add(sandbox); err != nil {
		return nil, fmt.Errorf("failed to add sandbox %+v into store: %v", sandbox, err)
	}

	return &runtime.RunPodSandboxResponse{PodSandboxId: id}, nil
}

func (c *criContainerdService) generateSandboxContainerSpec(id string, config *runtime.PodSandboxConfig,
	imageConfig *imagespec.ImageConfig) (*runtimespec.Spec, error) {
	// Creates a spec Generator with the default spec.
	// TODO(random-liu): [P1] Compare the default settings with docker and containerd default.
	spec, err := containerd.GenerateSpec()
	if err != nil {
		return nil, err
	}
	g := generate.NewFromSpec(spec)

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
		cgroupsPath := getCgroupsPath(config.GetLinux().GetCgroupParent(), id)
		g.SetLinuxCgroupsPath(cgroupsPath)
	}
	// When cgroup parent is not set, containerd-shim will create container in a child cgroup
	// of the cgroup itself is in.
	// TODO(random-liu): [P2] Set default cgroup path if cgroup parent is not specified.

	// Set namespace options.
	nsOptions := config.GetLinux().GetSecurityContext().GetNamespaceOptions()
	// TODO(random-liu): [P1] Create permanent network namespace, so that we could still cleanup
	// network namespace after sandbox container dies unexpectedly.
	// By default, all namespaces are enabled for the container, runc will create a new namespace
	// for it. By removing the namespace, the container will inherit the namespace of the runtime.
	if nsOptions.GetHostNetwork() {
		g.RemoveLinuxNamespace(string(runtimespec.NetworkNamespace)) // nolint: errcheck
		// TODO(random-liu): [P1] Figure out how to handle UTS namespace.
	}

	if nsOptions.GetHostPid() {
		g.RemoveLinuxNamespace(string(runtimespec.PIDNamespace)) // nolint: errcheck
	}

	if nsOptions.GetHostIpc() {
		g.RemoveLinuxNamespace(string(runtimespec.IPCNamespace)) // nolint: errcheck
	}

	// TODO(random-liu): [P1] Apply SeLinux options.

	// TODO(random-liu): [P1] Set user.

	// TODO(random-liu): [P1] Set supplemental group.

	// TODO(random-liu): [P1] Set privileged.

	// Add sysctls
	sysctls := config.GetLinux().GetSysctls()
	for key, value := range sysctls {
		g.AddLinuxSysctl(key, value)
	}

	// TODO(random-liu): [P2] Set apparmor and seccomp from annotations.

	g.SetLinuxResourcesCPUShares(uint64(defaultSandboxCPUshares))
	g.SetProcessOOMScoreAdj(int(defaultSandboxOOMAdj))

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
