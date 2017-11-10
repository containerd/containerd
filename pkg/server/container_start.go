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
	"time"

	"github.com/containerd/containerd"
	"github.com/golang/glog"
	"golang.org/x/net/context"
	"k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1/runtime"

	cio "github.com/kubernetes-incubator/cri-containerd/pkg/server/io"
	containerstore "github.com/kubernetes-incubator/cri-containerd/pkg/store/container"
)

// StartContainer starts the container.
func (c *criContainerdService) StartContainer(ctx context.Context, r *runtime.StartContainerRequest) (retRes *runtime.StartContainerResponse, retErr error) {
	container, err := c.containerStore.Get(r.GetContainerId())
	if err != nil {
		return nil, fmt.Errorf("an error occurred when try to find container %q: %v", r.GetContainerId(), err)
	}

	var startErr error
	// update container status in one transaction to avoid race with event monitor.
	if err := container.Status.UpdateSync(func(status containerstore.Status) (containerstore.Status, error) {
		// Always apply status change no matter startContainer fails or not. Because startContainer
		// may change container state no matter it fails or succeeds.
		startErr = c.startContainer(ctx, container, &status)
		return status, nil
	}); startErr != nil {
		return nil, startErr
	} else if err != nil {
		return nil, fmt.Errorf("failed to update container %q metadata: %v", container.ID, err)
	}
	return &runtime.StartContainerResponse{}, nil
}

// startContainer actually starts the container. The function needs to be run in one transaction. Any updates
// to the status passed in will be applied no matter the function returns error or not.
func (c *criContainerdService) startContainer(ctx context.Context,
	cntr containerstore.Container,
	status *containerstore.Status) (retErr error) {
	id := cntr.ID
	meta := cntr.Metadata
	container := cntr.Container
	config := meta.Config

	// Return error if container is not in created state.
	if status.State() != runtime.ContainerState_CONTAINER_CREATED {
		return fmt.Errorf("container %q is in %s state", id, criContainerStateToString(status.State()))
	}
	// Do not start the container when there is a removal in progress.
	if status.Removing {
		return fmt.Errorf("container %q is in removing state", id)
	}

	defer func() {
		if retErr != nil {
			// Set container to exited if fail to start.
			status.Pid = 0
			status.FinishedAt = time.Now().UnixNano()
			status.ExitCode = errorStartExitCode
			status.Reason = errorStartReason
			status.Message = retErr.Error()
		}
	}()

	// Get sandbox config from sandbox store.
	sandbox, err := c.sandboxStore.Get(meta.SandboxID)
	if err != nil {
		return fmt.Errorf("sandbox %q not found: %v", meta.SandboxID, err)
	}
	sandboxID := meta.SandboxID
	// Make sure sandbox is running.
	s, err := sandbox.Container.Task(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to get sandbox container %q info: %v", sandboxID, err)
	}
	// This is only a best effort check, sandbox may still exit after this. If sandbox fails
	// before starting the container, the start will fail.
	taskStatus, err := s.Status(ctx)
	if err != nil {
		return fmt.Errorf("failed to get task status for sandbox container %q: %v", id, err)
	}
	if taskStatus.Status != containerd.Running {
		return fmt.Errorf("sandbox container %q is not running", sandboxID)
	}

	ioCreation := func(id string) (_ containerd.IO, err error) {
		stdoutWC, stderrWC, err := createContainerLoggers(meta.LogPath, config.GetTty())
		if err != nil {
			return nil, fmt.Errorf("failed to create container loggers: %v", err)
		}
		defer func() {
			if err != nil {
				if stdoutWC != nil {
					stdoutWC.Close()
				}
				if stderrWC != nil {
					stderrWC.Close()
				}
			}
		}()
		if err := cio.WithOutput("log", stdoutWC, stderrWC)(cntr.IO); err != nil {
			return nil, fmt.Errorf("failed to add container log: %v", err)
		}
		cntr.IO.Pipe()
		return cntr.IO, nil
	}

	task, err := container.NewTask(ctx, ioCreation)
	if err != nil {
		return fmt.Errorf("failed to create containerd task: %v", err)
	}
	defer func() {
		if retErr != nil {
			if _, err := task.Delete(ctx, containerd.WithProcessKill); err != nil {
				glog.Errorf("Failed to delete containerd task %q: %v", id, err)
			}
		}
	}()

	// Start containerd task.
	if err := task.Start(ctx); err != nil {
		return fmt.Errorf("failed to start containerd task %q: %v", id, err)
	}

	// Update container start timestamp.
	status.Pid = task.Pid()
	status.StartedAt = time.Now().UnixNano()
	return nil
}

// Create container loggers and return write closer for stdout and stderr.
func createContainerLoggers(logPath string, tty bool) (stdout io.WriteCloser, stderr io.WriteCloser, err error) {
	if logPath != "" {
		// Only generate container log when log path is specified.
		if stdout, err = cio.NewCRILogger(logPath, cio.Stdout); err != nil {
			return nil, nil, fmt.Errorf("failed to start container stdout logger: %v", err)
		}
		defer func() {
			if err != nil {
				stdout.Close()
			}
		}()
		// Only redirect stderr when there is no tty.
		if !tty {
			if stderr, err = cio.NewCRILogger(logPath, cio.Stderr); err != nil {
				return nil, nil, fmt.Errorf("failed to start container stderr logger: %v", err)
			}
		}
	} else {
		stdout = cio.NewDiscardLogger()
		stderr = cio.NewDiscardLogger()
	}
	return
}
