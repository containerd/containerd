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
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/containerd/containerd/api/services/execution"
	"github.com/containerd/containerd/api/types/mount"
	"github.com/containerd/containerd/api/types/task"
	"github.com/golang/glog"
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
	sandboxInfo, err := c.taskService.Info(ctx, &execution.InfoRequest{ContainerID: sandboxID})
	if err != nil {
		return fmt.Errorf("failed to get sandbox container %q info: %v", sandboxID, err)
	}
	// This is only a best effort check, sandbox may still exit after this. If sandbox fails
	// before starting the container, the start will fail.
	if sandboxInfo.Task.Status != task.StatusRunning {
		return fmt.Errorf("sandbox container %q is not running", sandboxID)
	}

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
	rootfsMounts, err := c.snapshotService.Mounts(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to get rootfs mounts %q: %v", id, err)
	}
	var rootfs []*mount.Mount
	for _, m := range rootfsMounts {
		rootfs = append(rootfs, &mount.Mount{
			Type:    m.Type,
			Source:  m.Source,
			Options: m.Options,
		})
	}

	// Create containerd task.
	createOpts := &execution.CreateRequest{
		ContainerID: id,
		Rootfs:      rootfs,
		Stdin:       stdin,
		Stdout:      stdout,
		Stderr:      stderr,
		Terminal:    config.GetTty(),
	}
	glog.V(5).Infof("Create containerd task (id=%q, name=%q) with options %+v.",
		id, meta.Name, createOpts)
	createResp, err := c.taskService.Create(ctx, createOpts)
	if err != nil {
		return fmt.Errorf("failed to create containerd task: %v", err)
	}
	defer func() {
		if retErr != nil {
			// Cleanup the containerd task if an error is returned.
			if _, err := c.taskService.Delete(ctx, &execution.DeleteRequest{ContainerID: id}); err != nil {
				glog.Errorf("Failed to delete containerd task %q: %v", id, err)
			}
		}
	}()

	// Start containerd task.
	if _, err := c.taskService.Start(ctx, &execution.StartRequest{ContainerID: id}); err != nil {
		return fmt.Errorf("failed to start containerd task %q: %v", id, err)
	}

	// Update container start timestamp.
	meta.Pid = createResp.Pid
	meta.StartedAt = time.Now().UnixNano()
	return nil
}
