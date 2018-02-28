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
	"time"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/errdefs"
	"github.com/cri-o/ocicni/pkg/ocicni"
	"github.com/sirupsen/logrus"
	"golang.org/x/net/context"
	runtime "k8s.io/kubernetes/pkg/kubelet/apis/cri/runtime/v1alpha2"

	sandboxstore "github.com/containerd/cri/pkg/store/sandbox"
)

// StopPodSandbox stops the sandbox. If there are any running containers in the
// sandbox, they should be forcibly terminated.
func (c *criContainerdService) StopPodSandbox(ctx context.Context, r *runtime.StopPodSandboxRequest) (*runtime.StopPodSandboxResponse, error) {
	sandbox, err := c.sandboxStore.Get(r.GetPodSandboxId())
	if err != nil {
		return nil, fmt.Errorf("an error occurred when try to find sandbox %q: %v",
			r.GetPodSandboxId(), err)
	}
	// Use the full sandbox id.
	id := sandbox.ID

	// Stop all containers inside the sandbox. This terminates the container forcibly,
	// and container may still be so production should not rely on this behavior.
	// TODO(random-liu): Delete the sandbox container before this after permanent network namespace
	// is introduced, so that no container will be started after that.
	containers := c.containerStore.List()
	for _, container := range containers {
		if container.SandboxID != id {
			continue
		}
		// Forcibly stop the container. Do not use `StopContainer`, because it introduces a race
		// if a container is removed after list.
		if err = c.stopContainer(ctx, container, 0); err != nil {
			return nil, fmt.Errorf("failed to stop container %q: %v", container.ID, err)
		}
	}

	// Teardown network for sandbox.
	if sandbox.NetNSPath != "" && sandbox.NetNS != nil {
		if _, err := os.Stat(sandbox.NetNSPath); err != nil {
			if !os.IsNotExist(err) {
				return nil, fmt.Errorf("failed to stat network namespace path %s :%v", sandbox.NetNSPath, err)
			}
		} else {
			if teardownErr := c.netPlugin.TearDownPod(ocicni.PodNetwork{
				Name:         sandbox.Config.GetMetadata().GetName(),
				Namespace:    sandbox.Config.GetMetadata().GetNamespace(),
				ID:           id,
				NetNS:        sandbox.NetNSPath,
				PortMappings: toCNIPortMappings(sandbox.Config.GetPortMappings()),
			}); teardownErr != nil {
				return nil, fmt.Errorf("failed to destroy network for sandbox %q: %v", id, teardownErr)
			}
		}
		/*TODO:It is still possible that containerd crashes after we teardown the network, but before we remove the network namespace.
		In that case, we'll not be able to remove the sandbox anymore. The chance is slim, but we should be aware of that.
		In the future, once TearDownPod is idempotent, this will be fixed.*/

		//Close the sandbox network namespace if it was created
		if err = sandbox.NetNS.Remove(); err != nil {
			return nil, fmt.Errorf("failed to remove network namespace for sandbox %q:  %v", id, err)
		}
	}

	logrus.Infof("TearDown network for sandbox %q successfully", id)

	sandboxRoot := getSandboxRootDir(c.config.RootDir, id)
	if err := c.unmountSandboxFiles(sandboxRoot, sandbox.Config); err != nil {
		return nil, fmt.Errorf("failed to unmount sandbox files in %q: %v", sandboxRoot, err)
	}

	// Only stop sandbox container when it's running.
	if sandbox.Status.Get().State == sandboxstore.StateReady {
		if err := c.stopSandboxContainer(ctx, sandbox); err != nil {
			return nil, fmt.Errorf("failed to stop sandbox container %q: %v", id, err)
		}
	}
	return &runtime.StopPodSandboxResponse{}, nil
}

// stopSandboxContainer kills and deletes sandbox container.
func (c *criContainerdService) stopSandboxContainer(ctx context.Context, sandbox sandboxstore.Sandbox) error {
	container := sandbox.Container
	task, err := container.Task(ctx, nil)
	if err != nil {
		if errdefs.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to get sandbox container: %v", err)
	}

	// Delete the sandbox container from containerd.
	_, err = task.Delete(ctx, containerd.WithProcessKill)
	if err != nil && !errdefs.IsNotFound(err) {
		return fmt.Errorf("failed to delete sandbox container: %v", err)
	}

	return c.waitSandboxStop(ctx, sandbox, killContainerTimeout)
}

// waitSandboxStop waits for sandbox to be stopped until timeout exceeds or context is cancelled.
func (c *criContainerdService) waitSandboxStop(ctx context.Context, sandbox sandboxstore.Sandbox, timeout time.Duration) error {
	timeoutTimer := time.NewTimer(timeout)
	defer timeoutTimer.Stop()
	select {
	case <-ctx.Done():
		return fmt.Errorf("wait sandbox container %q is cancelled", sandbox.ID)
	case <-timeoutTimer.C:
		return fmt.Errorf("wait sandbox container %q stop timeout", sandbox.ID)
	case <-sandbox.Stopped():
		return nil
	}
}
