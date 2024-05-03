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

	apitasks "github.com/containerd/containerd/api/services/tasks/v1"
	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/errdefs"
	"github.com/containerd/log"
)

func (c *Controller) Shutdown(ctx context.Context, sandboxID string) error {
	sandbox := c.store.Get(sandboxID)
	if sandbox == nil {
		// Do not return error if the id doesn't exist.
		log.G(ctx).Tracef("Sandbox controller Delete called for sandbox %q that does not exist", sandboxID)
		return nil
	}

	// Cleanup the sandbox root directories.
	sandboxRootDir := c.getSandboxRootDir(sandboxID)
	if err := ensureRemoveAll(ctx, sandboxRootDir); err != nil {
		return fmt.Errorf("failed to remove sandbox root directory %q: %w", sandboxRootDir, err)
	}
	volatileSandboxRootDir := c.getVolatileSandboxRootDir(sandboxID)
	if err := ensureRemoveAll(ctx, volatileSandboxRootDir); err != nil {
		return fmt.Errorf("failed to remove volatile sandbox root directory %q: %w",
			volatileSandboxRootDir, err)
	}

	// Delete sandbox container.
	if sandbox.Container != nil {
		if err := c.cleanupSandboxTask(ctx, sandbox.Container); err != nil {
			return fmt.Errorf("failed to delete sandbox task %q: %w", sandboxID, err)
		}

		if err := sandbox.Container.Delete(ctx, containerd.WithSnapshotCleanup); err != nil {
			if !errdefs.IsNotFound(err) {
				return fmt.Errorf("failed to delete sandbox container %q: %w", sandboxID, err)
			}
			log.G(ctx).Tracef("Sandbox controller Delete called for sandbox container %q that does not exist", sandboxID)
		}
	}

	c.store.Remove(sandboxID)

	return nil
}

func (c *Controller) cleanupSandboxTask(ctx context.Context, sbCntr containerd.Container) error {
	task, err := sbCntr.Task(ctx, nil)
	if err != nil {
		if !errdefs.IsNotFound(err) {
			return fmt.Errorf("failed to load task for sandbox: %w", err)
		}
	} else {
		if _, err = task.Delete(ctx, containerd.WithProcessKill); err != nil {
			if !errdefs.IsNotFound(err) {
				return fmt.Errorf("failed to stop sandbox: %w", err)
			}
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
	// Based on containerd/containerd#7496 issue, when host is under IO
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
		_, err = c.client.TaskService().Delete(ctx, &apitasks.DeleteTaskRequest{ContainerID: sbCntr.ID()})
		if err != nil {
			err = errdefs.FromGRPC(err)
			if !errdefs.IsNotFound(err) {
				return fmt.Errorf("failed to cleanup sandbox %s in task-service: %w", sbCntr.ID(), err)
			}
		}
		log.G(ctx).Infof("Ensure that sandbox %s in task-service has been cleanup successfully", sbCntr.ID())
	}
	return nil
}
