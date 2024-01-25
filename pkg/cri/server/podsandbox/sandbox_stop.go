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
	"time"

	"github.com/containerd/log"

	eventtypes "github.com/containerd/containerd/v2/api/events"
	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/sandbox"
	"github.com/containerd/containerd/v2/pkg/cri/server/podsandbox/types"
	sandboxstore "github.com/containerd/containerd/v2/pkg/cri/store/sandbox"
	ctrdutil "github.com/containerd/containerd/v2/pkg/cri/util"
	"github.com/containerd/containerd/v2/protobuf"
	"github.com/containerd/errdefs"
)

func (c *Controller) Stop(ctx context.Context, sandboxID string, _ ...sandbox.StopOpt) error {
	podSandbox := c.store.Get(sandboxID)
	if podSandbox == nil {
		return errdefs.ErrNotFound
	}
	if podSandbox.Container == nil {
		return nil
	}
	meta, err := getMetadata(ctx, podSandbox.Container)
	if err != nil {
		return err
	}
	state := podSandbox.State
	if state == sandboxstore.StateReady || state == sandboxstore.StateUnknown {
		if err := c.stopSandboxContainer(ctx, podSandbox); err != nil {
			return fmt.Errorf("failed to stop sandbox container %q in %q state: %w", sandboxID, state, err)
		}
	}
	if err := c.cleanupSandboxFiles(sandboxID, meta.Config); err != nil {
		return fmt.Errorf("failed to cleanup sandbox files: %w", err)
	}
	return nil
}

// stopSandboxContainer kills the sandbox container.
// `task.Delete` is not called here because it will be called when
// the event monitor handles the `TaskExit` event.
func (c *Controller) stopSandboxContainer(ctx context.Context, podSandbox *types.PodSandbox) error {
	id := podSandbox.ID
	container := podSandbox.Container
	state := podSandbox.State
	task, err := container.Task(ctx, nil)
	if err != nil {
		if !errdefs.IsNotFound(err) {
			return fmt.Errorf("failed to get pod sandbox container: %w", err)
		}
		// Don't return for unknown state, some cleanup needs to be done.
		if state == sandboxstore.StateUnknown {
			return cleanupUnknownSandbox(ctx, id, podSandbox)
		}
		return nil
	}

	// Handle unknown state.
	// The cleanup logic is the same with container unknown state.
	if state == sandboxstore.StateUnknown {
		// Start an exit handler for sandbox container in unknown state.
		waitCtx, waitCancel := context.WithCancel(ctrdutil.NamespacedContext())
		defer waitCancel()
		exitCh, err := task.Wait(waitCtx)
		if err != nil {
			if !errdefs.IsNotFound(err) {
				return fmt.Errorf("failed to wait for task: %w", err)
			}
			return cleanupUnknownSandbox(ctx, id, podSandbox)
		}

		exitCtx, exitCancel := context.WithCancel(context.Background())
		stopCh := make(chan struct{})
		go func() {
			defer close(stopCh)
			exitStatus, exitedAt, err := c.waitSandboxExit(exitCtx, podSandbox, exitCh)
			if err != context.Canceled && err != context.DeadlineExceeded {
				// The error of context.Canceled or context.DeadlineExceeded indicates the task.Wait is not finished,
				// so we can not set the exit status of the pod sandbox.
				podSandbox.Exit(*containerd.NewExitStatus(exitStatus, exitedAt, err))
			} else {
				log.G(ctx).WithError(err).Errorf("Failed to wait pod sandbox exit %+v", err)
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

	// Kill the pod sandbox container.
	if err = task.Kill(ctx, syscall.SIGKILL); err != nil && !errdefs.IsNotFound(err) {
		return fmt.Errorf("failed to kill pod sandbox container: %w", err)
	}

	_, err = podSandbox.Wait(ctx)
	return err
}

// cleanupUnknownSandbox cleanup stopped sandbox in unknown state.
func cleanupUnknownSandbox(ctx context.Context, id string, sandbox *types.PodSandbox) error {
	// Reuse handleSandboxTaskExit to do the cleanup.
	return handleSandboxTaskExit(ctx, sandbox, &eventtypes.TaskExit{ExitStatus: unknownExitCode, ExitedAt: protobuf.ToTimestamp(time.Now())})
}
