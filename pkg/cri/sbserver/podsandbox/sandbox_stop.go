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
	"context"
	"fmt"
	"syscall"

	eventtypes "github.com/containerd/containerd/api/events"
	api "github.com/containerd/containerd/api/services/sandbox/v1"
	"github.com/containerd/containerd/errdefs"
	sandboxstore "github.com/containerd/containerd/pkg/cri/store/sandbox"
	ctrdutil "github.com/containerd/containerd/pkg/cri/util"
	"github.com/containerd/containerd/protobuf"

	"github.com/sirupsen/logrus"
)

func (c *Controller) Stop(ctx context.Context, sandboxID string) (*api.ControllerStopResponse, error) {
	sandbox, err := c.sandboxStore.Get(sandboxID)
	if err != nil {
		return nil, fmt.Errorf("an error occurred when try to find sandbox %q: %w",
			sandboxID, err)
	}

	if err := c.cleanupSandboxFiles(sandboxID, sandbox.Config); err != nil {
		return nil, fmt.Errorf("failed to cleanup sandbox files: %w", err)
	}

	// TODO: The Controller maintains its own Status instead of CRI's sandboxStore.
	// Only stop sandbox container when it's running or unknown.
	state := sandbox.Status.Get().State
	if state == sandboxstore.StateReady || state == sandboxstore.StateUnknown {
		if err := c.stopSandboxContainer(ctx, sandbox); err != nil {
			return nil, fmt.Errorf("failed to stop sandbox container %q in %q state: %w", sandboxID, state, err)
		}
	}
	return &api.ControllerStopResponse{}, nil
}

// stopSandboxContainer kills the sandbox container.
// `task.Delete` is not called here because it will be called when
// the event monitor handles the `TaskExit` event.
func (c *Controller) stopSandboxContainer(ctx context.Context, sandbox sandboxstore.Sandbox) error {
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
			return cleanupUnknownSandbox(ctx, id, sandbox)
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
			return cleanupUnknownSandbox(ctx, id, sandbox)
		}

		exitCtx, exitCancel := context.WithCancel(context.Background())
		stopCh := make(chan struct{})
		go func() {
			defer close(stopCh)
			exitStatus, exitedAt, err := c.waitSandboxExit(exitCtx, id, exitCh)
			if err != nil && err != context.Canceled && err != context.DeadlineExceeded {
				e := &eventtypes.TaskExit{
					ContainerID: id,
					ID:          id,
					Pid:         task.Pid(),
					ExitStatus:  exitStatus,
					ExitedAt:    protobuf.ToTimestamp(exitedAt),
				}
				logrus.WithError(err).Errorf("Failed to wait sandbox exit %+v", e)
				// TODO: how to backoff
				c.cri.BackOffEvent(id, e)
			}
		}()
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
func (c *Controller) waitSandboxStop(ctx context.Context, sandbox sandboxstore.Sandbox) error {
	select {
	case <-ctx.Done():
		return fmt.Errorf("wait sandbox container %q: %w", sandbox.ID, ctx.Err())
	case <-sandbox.Stopped():
		return nil
	}
}

// cleanupUnknownSandbox cleanup stopped sandbox in unknown state.
func cleanupUnknownSandbox(ctx context.Context, id string, sandbox sandboxstore.Sandbox) error {
	// Reuse handleSandboxExit to do the cleanup.
	return handleSandboxExit(ctx, sandbox)
}
