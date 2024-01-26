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
	"fmt"
	"sync/atomic"
	"syscall"
	"time"

	eventtypes "github.com/containerd/containerd/v2/api/events"
	"github.com/containerd/containerd/v2/errdefs"
	containerstore "github.com/containerd/containerd/v2/pkg/cri/store/container"
	ctrdutil "github.com/containerd/containerd/v2/pkg/cri/util"
	"github.com/containerd/containerd/v2/protobuf"
	"github.com/containerd/log"

	"github.com/moby/sys/signal"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// StopContainer stops a running container with a grace period (i.e., timeout).
func (c *criService) StopContainer(ctx context.Context, r *runtime.StopContainerRequest) (*runtime.StopContainerResponse, error) {
	start := time.Now()
	// Get container config from container store.
	container, err := c.containerStore.Get(r.GetContainerId())
	if err != nil {
		return nil, fmt.Errorf("an error occurred when try to find container %q: %w", r.GetContainerId(), err)
	}

	if err := c.stopContainer(ctx, container, time.Duration(r.GetTimeout())*time.Second); err != nil {
		return nil, err
	}

	sandbox, err := c.sandboxStore.Get(container.SandboxID)
	if err != nil {
		err = c.nri.StopContainer(ctx, nil, &container)
	} else {
		err = c.nri.StopContainer(ctx, &sandbox, &container)
	}
	if err != nil {
		log.G(ctx).WithError(err).Error("NRI failed to stop container")
	}

	i, err := container.Container.Info(ctx)
	if err != nil {
		return nil, fmt.Errorf("get container info: %w", err)
	}

	containerStopTimer.WithValues(i.Runtime.Name).UpdateSince(start)

	return &runtime.StopContainerResponse{}, nil
}

// stopContainer stops a container based on the container metadata.
func (c *criService) stopContainer(ctx context.Context, container containerstore.Container, timeout time.Duration) error {
	id := container.ID
	sandboxID := container.SandboxID

	// Return without error if container is not running. This makes sure that
	// stop only takes real action after the container is started.
	state := container.Status.Get().State()
	if state != runtime.ContainerState_CONTAINER_RUNNING &&
		state != runtime.ContainerState_CONTAINER_UNKNOWN {
		log.G(ctx).Infof("Container to stop %q must be in running or unknown state, current state %q",
			id, criContainerStateToString(state))
		return nil
	}

	task, err := container.Container.Task(ctx, nil)
	if err != nil {
		if !errdefs.IsNotFound(err) {
			return fmt.Errorf("failed to get task for container %q: %w", id, err)
		}
		// Don't return for unknown state, some cleanup needs to be done.
		if state == runtime.ContainerState_CONTAINER_UNKNOWN {
			return cleanupUnknownContainer(ctx, id, container, sandboxID, c)
		}
		return nil
	}

	// Handle unknown state.
	if state == runtime.ContainerState_CONTAINER_UNKNOWN {
		// Start an exit handler for containers in unknown state.
		waitCtx, waitCancel := context.WithCancel(ctrdutil.NamespacedContext())
		defer waitCancel()
		exitCh, err := task.Wait(waitCtx)
		if err != nil {
			if !errdefs.IsNotFound(err) {
				return fmt.Errorf("failed to wait for task for %q: %w", id, err)
			}
			return cleanupUnknownContainer(ctx, id, container, sandboxID, c)
		}

		exitCtx, exitCancel := context.WithCancel(context.Background())
		stopCh := c.eventMonitor.startContainerExitMonitor(exitCtx, id, task.Pid(), exitCh)
		defer func() {
			exitCancel()
			// This ensures that exit monitor is stopped before
			// `Wait` is cancelled, so no exit event is generated
			// because of the `Wait` cancellation.
			<-stopCh
		}()
	}

	// We only need to kill the task. The event handler will Delete the
	// task from containerd after it handles the Exited event.
	if timeout > 0 {
		stopSignal := "SIGTERM"
		if container.StopSignal != "" {
			stopSignal = container.StopSignal
		} else {
			// The image may have been deleted, and the `StopSignal` field is
			// just introduced to handle that.
			// However, for containers created before the `StopSignal` field is
			// introduced, still try to get the stop signal from the image config.
			// If the image has been deleted, logging an error and using the
			// default SIGTERM is still better than returning error and leaving
			// the container unstoppable. (See issue #990)
			// TODO(random-liu): Remove this logic when containerd 1.2 is deprecated.
			image, err := c.GetImage(container.ImageRef)
			if err != nil {
				if !errdefs.IsNotFound(err) {
					return fmt.Errorf("failed to get image %q: %w", container.ImageRef, err)
				}
				log.G(ctx).Warningf("Image %q not found, stop container with signal %q", container.ImageRef, stopSignal)
			} else {
				if image.ImageSpec.Config.StopSignal != "" {
					stopSignal = image.ImageSpec.Config.StopSignal
				}
			}
		}
		sig, err := signal.ParseSignal(stopSignal)
		if err != nil {
			return fmt.Errorf("failed to parse stop signal %q: %w", stopSignal, err)
		}

		var sswt bool
		if container.IsStopSignaledWithTimeout == nil {
			log.G(ctx).Infof("unable to ensure stop signal %v was not sent twice to container %v", sig, id)
			sswt = true
		} else {
			sswt = atomic.CompareAndSwapUint32(container.IsStopSignaledWithTimeout, 0, 1)
		}

		if sswt {
			log.G(ctx).Infof("Stop container %q with signal %v", id, sig)
			if err = task.Kill(ctx, sig); err != nil && !errdefs.IsNotFound(err) {
				return fmt.Errorf("failed to stop container %q: %w", id, err)
			}
		} else {
			log.G(ctx).Infof("Skipping the sending of signal %v to container %q because a prior stop with timeout>0 request already sent the signal", sig, id)
		}

		sigTermCtx, sigTermCtxCancel := context.WithTimeout(ctx, timeout)
		defer sigTermCtxCancel()
		err = c.waitContainerStop(sigTermCtx, container)
		if err == nil {
			// Container stopped on first signal no need for SIGKILL
			return nil
		}
		// If the parent context was cancelled or exceeded return immediately
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// sigTermCtx was exceeded. Send SIGKILL
		log.G(ctx).Debugf("Stop container %q with signal %v timed out", id, sig)
	}

	log.G(ctx).Infof("Kill container %q", id)
	if err = task.Kill(ctx, syscall.SIGKILL); err != nil && !errdefs.IsNotFound(err) {
		return fmt.Errorf("failed to kill container %q: %w", id, err)
	}

	// Wait for a fixed timeout until container stop is observed by event monitor.
	err = c.waitContainerStop(ctx, container)
	if err != nil {
		return fmt.Errorf("an error occurs during waiting for container %q to be killed: %w", id, err)
	}
	return nil
}

// waitContainerStop waits for container to be stopped until context is
// cancelled or the context deadline is exceeded.
func (c *criService) waitContainerStop(ctx context.Context, container containerstore.Container) error {
	select {
	case <-ctx.Done():
		return fmt.Errorf("wait container %q: %w", container.ID, ctx.Err())
	case <-container.Stopped():
		return nil
	}
}

// cleanupUnknownContainer cleanup stopped container in unknown state.
func cleanupUnknownContainer(ctx context.Context, id string, cntr containerstore.Container, sandboxID string, c *criService) error {
	// Reuse handleContainerExit to do the cleanup.
	return handleContainerExit(ctx, &eventtypes.TaskExit{
		ContainerID: id,
		ID:          id,
		Pid:         0,
		ExitStatus:  unknownExitCode,
		ExitedAt:    protobuf.ToTimestamp(time.Now()),
	}, cntr, sandboxID, c)
}
