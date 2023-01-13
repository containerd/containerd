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

package server

import (
	"context"
	"errors"
	"fmt"
	"syscall"
	"time"

	eventtypes "github.com/containerd/containerd/api/events"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/protobuf"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	sandboxstore "github.com/containerd/containerd/pkg/cri/store/sandbox"
	ctrdutil "github.com/containerd/containerd/pkg/cri/util"
)

// StopPodSandbox stops the sandbox. If there are any running containers in the
// sandbox, they should be forcibly terminated.
func (c *criService) StopPodSandbox(ctx context.Context, r *runtime.StopPodSandboxRequest) (*runtime.StopPodSandboxResponse, error) {
	sandbox, err := c.sandboxStore.Get(r.GetPodSandboxId())
	if err != nil {
		return nil, fmt.Errorf("an error occurred when try to find sandbox %q: %w",
			r.GetPodSandboxId(), err)
	}

	if err := c.stopPodSandbox(ctx, sandbox); err != nil {
		return nil, err
	}

	return &runtime.StopPodSandboxResponse{}, nil
}

func (c *criService) stopPodSandbox(ctx context.Context, sandbox sandboxstore.Sandbox) error {
	// Use the full sandbox id.
	id := sandbox.ID

	// Stop all containers inside the sandbox. This terminates the container forcibly,
	// and container may still be created, so production should not rely on this behavior.
	// TODO(random-liu): Introduce a state in sandbox to avoid future container creation.
	stop := time.Now()
	containers := c.containerStore.List()
	for _, container := range containers {
		if container.SandboxID != id {
			continue
		}
		// Forcibly stop the container. Do not use `StopContainer`, because it introduces a race
		// if a container is removed after list.
		if err := c.stopContainer(ctx, container, 0); err != nil {
			return fmt.Errorf("failed to stop container %q: %w", container.ID, err)
		}
	}

	if err := c.cleanupSandboxFiles(id, sandbox.Config); err != nil {
		return fmt.Errorf("failed to cleanup sandbox files: %w", err)
	}

	// Only stop sandbox container when it's running or unknown.
	state := sandbox.Status.Get().State
	if state == sandboxstore.StateReady || state == sandboxstore.StateUnknown {
		if err := c.stopSandboxContainer(ctx, sandbox); err != nil {
			return fmt.Errorf("failed to stop sandbox container %q in %q state: %w", id, state, err)
		}
	}
	sandboxRuntimeStopTimer.WithValues(sandbox.RuntimeHandler).UpdateSince(stop)

	err := c.nri.StopPodSandbox(ctx, &sandbox)
	if err != nil {
		log.G(ctx).WithError(err).Errorf("NRI sandbox stop notification failed")
	}

	// Teardown network for sandbox.
	if sandbox.NetNS != nil {
		netStop := time.Now()
		// Use empty netns path if netns is not available. This is defined in:
		// https://github.com/containernetworking/cni/blob/v0.7.0-alpha1/SPEC.md
		if closed, err := sandbox.NetNS.Closed(); err != nil {
			return fmt.Errorf("failed to check network namespace closed: %w", err)
		} else if closed {
			sandbox.NetNSPath = ""
		}
		if err := c.teardownPodNetwork(ctx, sandbox); err != nil {
			return fmt.Errorf("failed to destroy network for sandbox %q: %w", id, err)
		}
		if err := sandbox.NetNS.Remove(); err != nil {
			return fmt.Errorf("failed to remove network namespace for sandbox %q: %w", id, err)
		}
		sandboxDeleteNetwork.UpdateSince(netStop)
	}

	log.G(ctx).Infof("TearDown network for sandbox %q successfully", id)

	return nil
}

// stopSandboxContainer kills the sandbox container.
// `task.Delete` is not called here because it will be called when
// the event monitor handles the `TaskExit` event.
func (c *criService) stopSandboxContainer(ctx context.Context, sandbox sandboxstore.Sandbox) error {
	id := sandbox.ID
	container := sandbox.Container
	state := sandbox.Status.Get().State
	task, err := container.Task(ctx, nil)
	if err != nil {
		if !errdefs.IsNotFound(err) {
			return fmt.Errorf("failed to get sandbox container: %w", err)
		}
		// Don't return for unknown state, some cleanup needs to be done.
		if state == sandboxstore.StateUnknown {
			return c.cleanupUnknownSandbox(ctx, id, sandbox)
		}
		return nil
	}

	// Handle unknown state.
	// The cleanup logic is the same with container unknown state.
	if state == sandboxstore.StateUnknown {
		// Start an exit handler for containers in unknown state.
		waitCtx, waitCancel := context.WithCancel(ctrdutil.NamespacedContext())
		defer waitCancel()
		exitCh, err := task.Wait(waitCtx)
		if err != nil {
			if !errdefs.IsNotFound(err) {
				return fmt.Errorf("failed to wait for task: %w", err)
			}
			return c.cleanupUnknownSandbox(ctx, id, sandbox)
		}

		exitCtx, exitCancel := context.WithCancel(context.Background())
		stopCh := c.eventMonitor.startSandboxExitMonitor(exitCtx, id, task.Pid(), exitCh)
		defer func() {
			exitCancel()
			// This ensures that exit monitor is stopped before
			// `Wait` is cancelled, so no exit event is generated
			// because of the `Wait` cancellation.
			<-stopCh
		}()
	}

	// Kill the sandbox container.
	if err = task.Kill(ctx, syscall.SIGKILL); err != nil && !errdefs.IsNotFound(err) {
		return fmt.Errorf("failed to kill sandbox container: %w", err)
	}

	return c.waitSandboxStop(ctx, sandbox)
}

// waitSandboxStop waits for sandbox to be stopped until context is cancelled or
// the context deadline is exceeded.
func (c *criService) waitSandboxStop(ctx context.Context, sandbox sandboxstore.Sandbox) error {
	select {
	case <-ctx.Done():
		return fmt.Errorf("wait sandbox container %q: %w", sandbox.ID, ctx.Err())
	case <-sandbox.Stopped():
		return nil
	}
}

// teardownPodNetwork removes the network from the pod
func (c *criService) teardownPodNetwork(ctx context.Context, sandbox sandboxstore.Sandbox) error {
	netPlugin := c.getNetworkPlugin(sandbox.RuntimeHandler)
	if netPlugin == nil {
		return errors.New("cni config not initialized")
	}

	var (
		id     = sandbox.ID
		path   = sandbox.NetNSPath
		config = sandbox.Config
	)
	opts, err := cniNamespaceOpts(id, config)
	if err != nil {
		return fmt.Errorf("get cni namespace options: %w", err)
	}

	netStart := time.Now()
	err = netPlugin.Remove(ctx, id, path, opts...)
	networkPluginOperations.WithValues(networkTearDownOp).Inc()
	networkPluginOperationsLatency.WithValues(networkTearDownOp).UpdateSince(netStart)
	if err != nil {
		networkPluginOperationsErrors.WithValues(networkTearDownOp).Inc()
		return err
	}
	return nil
}

// cleanupUnknownSandbox cleanup stopped sandbox in unknown state.
func (c *criService) cleanupUnknownSandbox(ctx context.Context, id string, sandbox sandboxstore.Sandbox) error {
	// Reuse handleSandboxExit to do the cleanup.
	return handleSandboxExit(ctx, &eventtypes.TaskExit{
		ContainerID: id,
		ID:          id,
		Pid:         0,
		ExitStatus:  unknownExitCode,
		ExitedAt:    protobuf.ToTimestamp(time.Now()),
	}, sandbox, c)
}
