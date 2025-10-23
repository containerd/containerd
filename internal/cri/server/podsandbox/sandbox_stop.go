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

	"github.com/containerd/errdefs"
	"github.com/containerd/log"

	eventtypes "github.com/containerd/containerd/api/events"
	"github.com/containerd/containerd/v2/core/sandbox"
	"github.com/containerd/containerd/v2/internal/cri/server/podsandbox/types"
	sandboxstore "github.com/containerd/containerd/v2/internal/cri/store/sandbox"
	ctrdutil "github.com/containerd/containerd/v2/internal/cri/util"
	"github.com/containerd/containerd/v2/pkg/protobuf"
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
	state := podSandbox.Status.Get().State
	if state == sandboxstore.StateReady || state == sandboxstore.StateUnknown {
		if err := c.stopSandboxContainerRetryOnConnectionClosed(ctx, podSandbox); err != nil {
			return fmt.Errorf("failed to stop sandbox container %q in %q state: %w", sandboxID, state, err)
		}
	}
	if err := c.cleanupSandboxFiles(sandboxID, meta.Config); err != nil {
		return fmt.Errorf("failed to cleanup sandbox files: %w", err)
	}
	return nil
}

func (c *Controller) stopSandboxContainerRetryOnConnectionClosed(ctx context.Context, podSandbox *types.PodSandbox) error {
	const maxRetries = 3

	var err error
	for i := 1; i <= maxRetries; i++ {
		err = c.stopSandboxContainer(ctx, podSandbox)
		if err == nil {
			return nil
		}

		if !ctrdutil.IsShimTTRPCClosed(err) {
			return err
		}

		if i+1 <= maxRetries {
			retryAfter := time.Duration(100*i*i) * time.Millisecond
			log.G(ctx).WithError(err).
				Warnf("Shim ttrpc connection closed when stopping sandbox container %q, retry after %s", podSandbox.ID, retryAfter)
			time.Sleep(retryAfter)
		}
	}
	return err
}

// stopSandboxContainer kills the sandbox container.
// `task.Delete` is not called here because it will be called when
// the event monitor handles the `TaskExit` event.
func (c *Controller) stopSandboxContainer(ctx context.Context, podSandbox *types.PodSandbox) error {
	id := podSandbox.ID
	container := podSandbox.Container
	state := podSandbox.Status.Get().State
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
			err := c.waitSandboxExit(exitCtx, podSandbox, exitCh)
			if err != nil && err != context.Canceled && err != context.DeadlineExceeded {
				log.G(ctx).WithError(err).Errorf("Failed to wait sandbox exit %+v", err)
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
