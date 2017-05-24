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
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/containerd/containerd/api/services/execution"
	rootfsapi "github.com/containerd/containerd/api/services/rootfs"
	"github.com/containerd/containerd/api/types/container"
	prototypes "github.com/gogo/protobuf/types"
	"github.com/golang/glog"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/opencontainers/runtime-tools/generate"
	"golang.org/x/net/context"
	runtime "k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1"

	"github.com/kubernetes-incubator/cri-containerd/pkg/metadata"
	"github.com/kubernetes-incubator/cri-containerd/pkg/server/agents"
)

// StartContainer starts the container.
func (c *criContainerdService) StartContainer(ctx context.Context, r *runtime.StartContainerRequest) (retRes *runtime.StartContainerResponse, retErr error) {
	glog.V(2).Infof("StartContainer for %q", r.GetContainerId())
	defer func() {
		if retErr == nil {
			glog.V(2).Infof("StartContainer %q returns successfully", r.GetContainerId())
		}
	}()

	container, err := c.containerStore.Get(r.GetContainerId())
	if err != nil {
		return nil, fmt.Errorf("an error occurred when try to find container %q: %v", r.GetContainerId(), err)
	}
	id := container.ID

	var startErr error
	// start container in one transaction to avoid race with event monitor.
	if err := c.containerStore.Update(id, func(meta metadata.ContainerMetadata) (metadata.ContainerMetadata, error) {
		// Always apply metadata change no matter startContainer fails or not. Because startContainer
		// may change container state no matter it fails or succeeds.
		startErr = c.startContainer(ctx, id, &meta)
		return meta, nil
	}); startErr != nil {
		return nil, startErr
	} else if err != nil {
		return nil, fmt.Errorf("failed to update container %q metadata: %v", id, err)
	}
	return &runtime.StartContainerResponse{}, nil
}

// startContainer actually starts the container. The function needs to be run in one transaction. Any updates
// to the metadata passed in will be applied to container store no matter the function returns error or not.
func (c *criContainerdService) startContainer(ctx context.Context, id string, meta *metadata.ContainerMetadata) (retErr error) {
	config := meta.Config
	// Return error if container is not in created state.
	if meta.State() != runtime.ContainerState_CONTAINER_CREATED {
		return fmt.Errorf("container %q is in %s state", id, criContainerStateToString(meta.State()))
	}

	// Do not start the container when there is a removal in progress.
	if meta.Removing {
		return fmt.Errorf("container %q is in removing state", id)
	}

	defer func() {
		if retErr != nil {
			// Set container to exited if fail to start.
			meta.Pid = 0
			meta.FinishedAt = time.Now().UnixNano()
			meta.ExitCode = errorStartExitCode
			meta.Reason = errorStartReason
			meta.Message = retErr.Error()
		}
	}()

	// Get sandbox config from sandbox store.
	sandboxMeta, err := c.getSandbox(meta.SandboxID)
	if err != nil {
		return fmt.Errorf("sandbox %q not found: %v", meta.SandboxID, err)
	}
	sandboxConfig := sandboxMeta.Config
	sandboxID := meta.SandboxID
	// Make sure sandbox is running.
	sandboxInfo, err := c.containerService.Info(ctx, &execution.InfoRequest{ID: sandboxID})
	if err != nil {
		return fmt.Errorf("failed to get sandbox container %q info: %v", sandboxID, err)
	}
	// This is only a best effort check, sandbox may still exit after this. If sandbox fails
	// before starting the container, the start will fail.
	if sandboxInfo.Status != container.Status_RUNNING {
		return fmt.Errorf("sandbox container %q is not running", sandboxID)
	}
	sandboxPid := sandboxInfo.Pid
	glog.V(2).Infof("Sandbox container %q is running with pid %d", sandboxID, sandboxPid)

	// Generate containerd container create options.
	imageMeta, err := c.imageMetadataStore.Get(meta.ImageRef)
	if err != nil {
		return fmt.Errorf("failed to get container image %q: %v", meta.ImageRef, err)
	}

	mounts := c.generateContainerMounts(getSandboxRootDir(c.rootDir, sandboxID), config)

	spec, err := c.generateContainerSpec(id, sandboxPid, config, sandboxConfig, imageMeta.Config, mounts)
	if err != nil {
		return fmt.Errorf("failed to generate container %q spec: %v", id, err)
	}
	rawSpec, err := json.Marshal(spec)
	if err != nil {
		return fmt.Errorf("failed to marshal oci spec %+v: %v", spec, err)
	}
	glog.V(4).Infof("Container spec: %+v", spec)

	containerRootDir := getContainerRootDir(c.rootDir, id)
	stdin, stdout, stderr := getStreamingPipes(containerRootDir)
	// Set stdin to empty if Stdin == false.
	if !config.GetStdin() {
		stdin = ""
	}
	stdinPipe, stdoutPipe, stderrPipe, err := c.prepareStreamingPipes(ctx, stdin, stdout, stderr)
	if err != nil {
		return fmt.Errorf("failed to prepare streaming pipes: %v", err)
	}
	defer func() {
		if retErr != nil {
			if stdinPipe != nil {
				stdinPipe.Close()
			}
			stdoutPipe.Close()
			stderrPipe.Close()
		}
	}()
	// Redirect the stream to std for now.
	// TODO(random-liu): [P1] Support container logging.
	// TODO(random-liu): [P1] Support StdinOnce after container logging is added.
	if stdinPipe != nil {
		go func(w io.WriteCloser) {
			io.Copy(w, os.Stdin) // nolint: errcheck
			w.Close()
		}(stdinPipe)
	}
	if config.GetLogPath() != "" {
		// Only generate container log when log path is specified.
		logPath := filepath.Join(sandboxConfig.GetLogDirectory(), config.GetLogPath())
		if err := c.agentFactory.NewContainerLogger(logPath, agents.Stdout, stdoutPipe).Start(); err != nil {
			return fmt.Errorf("failed to start container stdout logger: %v", err)
		}
		// Only redirect stderr when there is no tty.
		if !config.GetTty() {
			if err := c.agentFactory.NewContainerLogger(logPath, agents.Stderr, stderrPipe).Start(); err != nil {
				return fmt.Errorf("failed to start container stderr logger: %v", err)
			}
		}
	}

	// Get rootfs mounts.
	mountsResp, err := c.rootfsService.Mounts(ctx, &rootfsapi.MountsRequest{Name: id})
	if err != nil {
		return fmt.Errorf("failed to get rootfs mounts %q: %v", id, err)
	}

	// Create containerd container.
	createOpts := &execution.CreateRequest{
		ID: id,
		Spec: &prototypes.Any{
			TypeUrl: runtimespec.Version,
			Value:   rawSpec,
		},
		Rootfs:   mountsResp.Mounts,
		Runtime:  defaultRuntime,
		Stdin:    stdin,
		Stdout:   stdout,
		Stderr:   stderr,
		Terminal: config.GetTty(),
	}
	glog.V(5).Infof("Create containerd container (id=%q, name=%q) with options %+v.",
		id, meta.Name, createOpts)
	createResp, err := c.containerService.Create(ctx, createOpts)
	if err != nil {
		return fmt.Errorf("failed to create containerd container: %v", err)
	}
	defer func() {
		if retErr != nil {
			// Cleanup the containerd container if an error is returned.
			if _, err := c.containerService.Delete(ctx, &execution.DeleteRequest{ID: id}); err != nil {
				glog.Errorf("Failed to delete containerd container %q: %v", id, err)
			}
		}
	}()

	// Start containerd container.
	if _, err := c.containerService.Start(ctx, &execution.StartRequest{ID: id}); err != nil {
		return fmt.Errorf("failed to start containerd container %q: %v", id, err)
	}

	// Update container start timestamp.
	meta.Pid = createResp.Pid
	meta.StartedAt = time.Now().UnixNano()
	return nil
}

func (c *criContainerdService) generateContainerSpec(id string, sandboxPid uint32, config *runtime.ContainerConfig,
	sandboxConfig *runtime.PodSandboxConfig, imageConfig *imagespec.ImageConfig, extraMounts []*runtime.Mount) (*runtimespec.Spec, error) {
	// Creates a spec Generator with the default spec.
	// TODO(random-liu): [P2] Move container runtime spec generation into a helper function.
	g := generate.New()

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

	// Apply envs from image config first, so that envs from container config
	// can override them.
	if err := addImageEnvs(&g, imageConfig.Env); err != nil {
		return nil, err
	}
	for _, e := range config.GetEnvs() {
		g.AddProcessEnv(e.GetKey(), e.GetValue())
	}

	// Add extra mounts first so that CRI specified mounts can override.
	addOCIBindMounts(&g, append(extraMounts, config.GetMounts()...))

	// TODO(random-liu): [P1] Set device mapping.
	// Ref https://github.com/moby/moby/blob/master/oci/devices_linux.go.

	// TODO(random-liu): [P1] Handle container logging, decorate and redirect to file.

	setOCILinuxResource(&g, config.GetLinux().GetResources())

	if sandboxConfig.GetLinux().GetCgroupParent() != "" {
		cgroupsPath := getCgroupsPath(sandboxConfig.GetLinux().GetCgroupParent(), id)
		g.SetLinuxCgroupsPath(cgroupsPath)
	}

	g.SetProcessTerminal(config.GetTty())

	securityContext := config.GetLinux().GetSecurityContext()

	if err := setOCICapabilities(&g, securityContext.GetCapabilities()); err != nil {
		return nil, fmt.Errorf("failed to set capabilities %+v: %v",
			securityContext.GetCapabilities(), err)
	}

	// TODO(random-liu): [P0] Handle privileged.

	// Set namespaces, share namespace with sandbox container.
	setOCINamespaces(&g, securityContext.GetNamespaceOptions(), sandboxPid)

	// TODO(random-liu): [P1] Set selinux options.

	// TODO(random-liu): [P1] Set user/username.

	supplementalGroups := securityContext.GetSupplementalGroups()
	for _, group := range supplementalGroups {
		g.AddProcessAdditionalGid(uint32(group))
	}

	g.SetRootReadonly(securityContext.GetReadonlyRootfs())

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

	// TODO(random-liu): [P0] Mount sandbox /dev/shm.
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

// addOCIBindMounts adds bind mounts.
func addOCIBindMounts(g *generate.Generator, mounts []*runtime.Mount) {
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
}

// setOCILinuxResource set container resource limit.
func setOCILinuxResource(g *generate.Generator, resources *runtime.LinuxContainerResources) {
	if resources == nil {
		return
	}
	g.SetLinuxResourcesCPUPeriod(uint64(resources.GetCpuPeriod()))
	g.SetLinuxResourcesCPUQuota(resources.GetCpuQuota())
	g.SetLinuxResourcesCPUShares(uint64(resources.GetCpuShares()))
	g.SetLinuxResourcesMemoryLimit(uint64(resources.GetMemoryLimitInBytes()))
	g.SetLinuxResourcesOOMScoreAdj(int(resources.GetOomScoreAdj()))
}

// setOCICapabilities adds/drops process capabilities.
func setOCICapabilities(g *generate.Generator, capabilities *runtime.Capability) error {
	if capabilities == nil {
		return nil
	}

	for _, c := range capabilities.GetAddCapabilities() {
		if err := g.AddProcessCapability(c); err != nil {
			return err
		}
	}

	for _, c := range capabilities.GetDropCapabilities() {
		if err := g.DropProcessCapability(c); err != nil {
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
