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
	"time"

	"github.com/containerd/errdefs"
	"github.com/containerd/log"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	eventtypes "github.com/containerd/containerd/v2/api/events"
	apitasks "github.com/containerd/containerd/v2/api/services/tasks/v1"
	containerd "github.com/containerd/containerd/v2/client"
	containerstore "github.com/containerd/containerd/v2/internal/cri/store/container"
	sandboxstore "github.com/containerd/containerd/v2/internal/cri/store/sandbox"
	ctrdutil "github.com/containerd/containerd/v2/internal/cri/util"
	containerdio "github.com/containerd/containerd/v2/pkg/cio"
	"github.com/containerd/containerd/v2/protobuf"
)

const (
	// handleEventTimeout is the timeout for handling 1 event. Event monitor
	// handles events in serial, if one event blocks the event monitor, no
	// other events can be handled.
	// Add a timeout for each event handling, events that timeout will be requeued and
	// handled again in the future.
	handleEventTimeout = 10 * time.Second
)

// startSandboxExitMonitor starts an exit monitor for a given sandbox.
func (c *criService) startSandboxExitMonitor(ctx context.Context, id string, exitCh <-chan containerd.ExitStatus) <-chan struct{} {
	stopCh := make(chan struct{})
	go func() {
		defer close(stopCh)
		select {
		case exitRes := <-exitCh:
			exitStatus, exitedAt, err := exitRes.Result()
			if err != nil {
				log.L.WithError(err).Errorf("failed to get task exit status for %q", id)
				exitStatus = unknownExitCode
				exitedAt = time.Now()
			}

			e := &eventtypes.SandboxExit{
				SandboxID:  id,
				ExitStatus: exitStatus,
				ExitedAt:   protobuf.ToTimestamp(exitedAt),
			}

			log.L.Debugf("received exit event %+v", e)

			err = func() error {
				dctx := ctrdutil.NamespacedContext()
				dctx, dcancel := context.WithTimeout(dctx, handleEventTimeout)
				defer dcancel()

				sb, err := c.sandboxStore.Get(id)
				if err == nil {
					if err := c.handleSandboxExit(dctx, sb, exitStatus, exitedAt); err != nil {
						return err
					}
					return nil
				} else if !errdefs.IsNotFound(err) {
					return fmt.Errorf("failed to get sandbox %s: %w", e.SandboxID, err)
				}
				return nil
			}()
			if err != nil {
				log.L.WithError(err).Errorf("failed to handle sandbox TaskExit event %+v", e)
				c.eventMonitor.Backoff(id, e)
			}
			return
		case <-ctx.Done():
		}
	}()
	return stopCh
}

// handleSandboxExit handles sandbox exit event.
func (c *criService) handleSandboxExit(ctx context.Context, sb sandboxstore.Sandbox, exitStatus uint32, exitTime time.Time) error {
	if err := sb.Status.Update(func(status sandboxstore.Status) (sandboxstore.Status, error) {
		status.State = sandboxstore.StateNotReady
		status.Pid = 0
		status.ExitStatus = exitStatus
		status.ExitedAt = exitTime
		return status, nil
	}); err != nil {
		return fmt.Errorf("failed to update sandbox state: %w", err)
	}

	// Using channel to propagate the information of sandbox stop
	sb.Stop()
	c.generateAndSendContainerEvent(ctx, sb.ID, sb.ID, runtime.ContainerEventType_CONTAINER_STOPPED_EVENT)
	return nil
}

// startContainerExitMonitor starts an exit monitor for a given container.
func (c *criService) startContainerExitMonitor(ctx context.Context, id string, pid uint32, exitCh <-chan containerd.ExitStatus) <-chan struct{} {
	stopCh := make(chan struct{})
	go func() {
		defer close(stopCh)
		select {
		case exitRes := <-exitCh:
			exitStatus, exitedAt, err := exitRes.Result()
			if err != nil {
				log.L.WithError(err).Errorf("failed to get task exit status for %q", id)
				exitStatus = unknownExitCode
				exitedAt = time.Now()
			}

			e := &eventtypes.TaskExit{
				ContainerID: id,
				ID:          id,
				Pid:         pid,
				ExitStatus:  exitStatus,
				ExitedAt:    protobuf.ToTimestamp(exitedAt),
			}

			log.L.Debugf("received exit event %+v", e)

			err = func() error {
				dctx := ctrdutil.NamespacedContext()
				dctx, dcancel := context.WithTimeout(dctx, handleEventTimeout)
				defer dcancel()

				cntr, err := c.containerStore.Get(e.ID)
				if err == nil {
					if err := c.handleContainerExit(dctx, e, cntr, cntr.SandboxID); err != nil {
						return err
					}
					return nil
				} else if !errdefs.IsNotFound(err) {
					return fmt.Errorf("failed to get container %s: %w", e.ID, err)
				}
				return nil
			}()
			if err != nil {
				log.L.WithError(err).Errorf("failed to handle container TaskExit event %+v", e)
				c.eventMonitor.Backoff(id, e)
			}
			return
		case <-ctx.Done():
		}
	}()
	return stopCh
}

// handleContainerExit handles TaskExit event for container.
func (c *criService) handleContainerExit(ctx context.Context, e *eventtypes.TaskExit, cntr containerstore.Container, sandboxID string) error {
	// Attach container IO so that `Delete` could cleanup the stream properly.
	task, err := cntr.Container.Task(ctx,
		func(*containerdio.FIFOSet) (containerdio.IO, error) {
			// We can't directly return cntr.IO here, because
			// even if cntr.IO is nil, the cio.IO interface
			// is not.
			// See https://tour.golang.org/methods/12:
			//   Note that an interface value that holds a nil
			//   concrete value is itself non-nil.
			if cntr.IO != nil {
				return cntr.IO, nil
			}
			return nil, nil
		},
	)
	if err != nil {
		if !errdefs.IsNotFound(err) && !errdefs.IsUnavailable(err) {
			return fmt.Errorf("failed to load task for container: %w", err)
		}
	} else {
		// TODO(random-liu): [P1] This may block the loop, we may want to spawn a worker
		if _, err = task.Delete(ctx, c.nri.WithContainerExit(&cntr), containerd.WithProcessKill); err != nil {
			if !errdefs.IsNotFound(err) {
				return fmt.Errorf("failed to stop container: %w", err)
			}
			// Move on to make sure container status is updated.
		}
	}

	// NOTE: Both sb.Container.Task and task.Delete interface always ensures
	// that the status of target task. However, the interfaces return
	// ErrNotFound, which doesn't mean that the shim instance doesn't exist.
	//
	// There are two caches for task in containerd:
	//
	//   1. io.containerd.service.v1.tasks-service
	//   2. io.containerd.runtime.v2.task
	//
	// First one is to maintain the shim connection and shutdown the shim
	// in Delete API. And the second one is to maintain the lifecycle of
	// task in shim server.
	//
	// So, if the shim instance is running and task has been deleted in shim
	// server, the sb.Container.Task and task.Delete will receive the
	// ErrNotFound. If we don't delete the shim instance in io.containerd.service.v1.tasks-service,
	// shim will be leaky.
	//
	// Based on containerd/containerd/v2#7496 issue, when host is under IO
	// pressure, the umount2 syscall will take more than 10 seconds so that
	// the CRI plugin will cancel this task.Delete call. However, the shim
	// server isn't aware about this. After return from umount2 syscall, the
	// shim server continue delete the task record. And then CRI plugin
	// retries to delete task and retrieves ErrNotFound and marks it as
	// stopped. Therefore, The shim is leaky.
	//
	// It's hard to handle the connection lost or request canceled cases in
	// shim server. We should call Delete API to io.containerd.service.v1.tasks-service
	// to ensure that shim instance is shutdown.
	//
	// REF:
	// 1. https://github.com/containerd/containerd/issues/7496#issuecomment-1671100968
	// 2. https://github.com/containerd/containerd/issues/8931
	if errdefs.IsNotFound(err) {
		_, err = c.client.TaskService().Delete(ctx, &apitasks.DeleteTaskRequest{ContainerID: cntr.Container.ID()})
		if err != nil {
			err = errdefs.FromGRPC(err)
			if !errdefs.IsNotFound(err) {
				return fmt.Errorf("failed to cleanup container %s in task-service: %w", cntr.Container.ID(), err)
			}
		}
		log.L.Infof("Ensure that container %s in task-service has been cleanup successfully", cntr.Container.ID())
	}

	err = cntr.Status.UpdateSync(func(status containerstore.Status) (containerstore.Status, error) {
		if status.FinishedAt == 0 {
			status.Pid = 0
			status.FinishedAt = protobuf.FromTimestamp(e.ExitedAt).UnixNano()
			status.ExitCode = int32(e.ExitStatus)
		}

		// Unknown state can only transit to EXITED state, so we need
		// to handle unknown state here.
		if status.Unknown {
			log.L.Debugf("Container %q transited from UNKNOWN to EXITED", cntr.ID)
			status.Unknown = false
		}
		return status, nil
	})
	if err != nil {
		return fmt.Errorf("failed to update container state: %w", err)
	}
	// Using channel to propagate the information of container stop
	cntr.Stop()
	c.generateAndSendContainerEvent(ctx, cntr.ID, sandboxID, runtime.ContainerEventType_CONTAINER_STOPPED_EVENT)
	return nil
}

type criEventHandler struct {
	c *criService
}

// HandleEvent handles a containerd event.
func (ce *criEventHandler) HandleEvent(any interface{}) error {
	ctx := ctrdutil.NamespacedContext()
	ctx, cancel := context.WithTimeout(ctx, handleEventTimeout)
	defer cancel()

	switch e := any.(type) {
	case *eventtypes.TaskExit:
		log.L.Infof("TaskExit event %+v", e)
		// Use ID instead of ContainerID to rule out TaskExit event for exec.
		cntr, err := ce.c.containerStore.Get(e.ID)
		if err == nil {
			if err := ce.c.handleContainerExit(ctx, e, cntr, cntr.SandboxID); err != nil {
				return fmt.Errorf("failed to handle container TaskExit event: %w", err)
			}
			return nil
		} else if !errdefs.IsNotFound(err) {
			return fmt.Errorf("can't find container for TaskExit event: %w", err)
		}
		sb, err := ce.c.sandboxStore.Get(e.ID)
		if err == nil {
			if err := ce.c.handleSandboxExit(ctx, sb, e.ExitStatus, e.ExitedAt.AsTime()); err != nil {
				return fmt.Errorf("failed to handle sandbox TaskExit event: %w", err)
			}
			return nil
		} else if !errdefs.IsNotFound(err) {
			return fmt.Errorf("can't find sandbox for TaskExit event: %w", err)
		}
		return nil
	case *eventtypes.SandboxExit:
		log.L.Infof("SandboxExit event %+v", e)
		sb, err := ce.c.sandboxStore.Get(e.GetSandboxID())
		if err == nil {
			if err := ce.c.handleSandboxExit(ctx, sb, e.ExitStatus, e.ExitedAt.AsTime()); err != nil {
				return fmt.Errorf("failed to handle sandbox TaskExit event: %w", err)
			}
			return nil
		} else if !errdefs.IsNotFound(err) {
			return fmt.Errorf("can't find sandbox for TaskExit event: %w", err)
		}
		return nil
	case *eventtypes.TaskOOM:
		log.L.Infof("TaskOOM event %+v", e)
		// For TaskOOM, we only care which container it belongs to.
		cntr, err := ce.c.containerStore.Get(e.ContainerID)
		if err != nil {
			if !errdefs.IsNotFound(err) {
				return fmt.Errorf("can't find container for TaskOOM event: %w", err)
			}
			return nil
		}
		err = cntr.Status.UpdateSync(func(status containerstore.Status) (containerstore.Status, error) {
			status.Reason = oomExitReason
			return status, nil
		})
		if err != nil {
			return fmt.Errorf("failed to update container status for TaskOOM event: %w", err)
		}
	case *eventtypes.ImageCreate:
		log.L.Infof("ImageCreate event %+v", e)
		return ce.c.UpdateImage(ctx, e.Name)
	case *eventtypes.ImageUpdate:
		log.L.Infof("ImageUpdate event %+v", e)
		return ce.c.UpdateImage(ctx, e.Name)
	case *eventtypes.ImageDelete:
		log.L.Infof("ImageDelete event %+v", e)
		return ce.c.UpdateImage(ctx, e.Name)
	}

	return nil
}
