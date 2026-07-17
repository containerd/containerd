//go:build linux

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
	"errors"
	"fmt"
	"os"
	"time"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/errdefs"
)

const podCheckpointTaskPoll = 10 * time.Millisecond

type podCheckpointRuntimeTask interface {
	ID() string
	Pause(context.Context) error
	Resume(context.Context) error
	Status(context.Context) (containerd.Status, error)
}

// podCheckpointKey identifies a sandbox across containerd namespaces.
func podCheckpointKey(namespace, sandboxID string) string {
	return namespace + "/" + sandboxID
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

// podCheckpointTaskRecovery records requests made by this checkpoint which may
// still need undoing. Successful recovery removes them so retries cannot undo
// a later freeze or pause made by someone else.
type podCheckpointTaskRecovery struct {
	freezer podCgroupFreezer
	tasks   []podCheckpointRuntimeTask
}

func pausePodCheckpointTasks(ctx context.Context, freezer podCgroupFreezer, tasks []podCheckpointRuntimeTask) (podCheckpointTaskRecovery, error) {
	var recovery podCheckpointTaskRecovery
	requested, err := freezer.Freeze(ctx)
	if requested {
		recovery.freezer = freezer
	}
	if err != nil {
		return recovery, fmt.Errorf("failed to freeze pod cgroup before pausing containers: %w", err)
	}
	for _, task := range tasks {
		if err := ctx.Err(); err != nil {
			return recovery, fmt.Errorf("pod checkpoint canceled while pausing containers: %w", err)
		}
		status, err := task.Status(ctx)
		if err != nil {
			return recovery, fmt.Errorf("failed to inspect checkpoint container %q before pause: %w", task.ID(), err)
		}
		if status.Status != containerd.Running {
			return recovery, fmt.Errorf("checkpoint container %q has state %s before pause; expected %s", task.ID(), status.Status, containerd.Running)
		}
		// A failed Pause RPC may already have paused the task.
		recovery.tasks = append(recovery.tasks, task)
		if err := task.Pause(ctx); err != nil {
			return recovery, fmt.Errorf("failed to pause checkpoint container %q: %w", task.ID(), err)
		}
		status, err = task.Status(ctx)
		if err != nil {
			return recovery, fmt.Errorf("failed to verify paused checkpoint container %q: %w", task.ID(), err)
		}
		if status.Status != containerd.Paused {
			return recovery, fmt.Errorf("checkpoint container %q has state %s after pause; expected %s", task.ID(), status.Status, containerd.Paused)
		}
	}
	if err := freezer.Thaw(ctx); err != nil {
		return recovery, fmt.Errorf("failed to thaw pod cgroup after pausing containers: %w", err)
	}
	recovery.freezer = nil
	return recovery, nil
}

func (r *podCheckpointTaskRecovery) thaw(ctx context.Context) error {
	if r.freezer == nil {
		return nil
	}
	if err := r.freezer.Thaw(ctx); err != nil {
		return fmt.Errorf("failed to thaw pod cgroup during checkpoint recovery: %w", err)
	}
	r.freezer = nil
	return nil
}

func (r *podCheckpointTaskRecovery) recover(ctx context.Context) error {
	recoveryErrors := []error{r.thaw(ctx)}
	pending := r.tasks[:0]
	for _, task := range r.tasks {
		if err := resumePodCheckpointTask(ctx, task); err != nil {
			recoveryErrors = append(recoveryErrors, err)
			pending = append(pending, task)
		}
	}
	r.tasks = pending
	return errors.Join(recoveryErrors...)
}

func resumePodCheckpointTask(ctx context.Context, task podCheckpointRuntimeTask) error {
	ticker := time.NewTicker(podCheckpointTaskPoll)
	defer ticker.Stop()
	for {
		status, err := task.Status(ctx)
		if errdefs.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("failed to inspect checkpoint container %q during recovery: %w", task.ID(), err)
		}
		switch status.Status {
		case containerd.Running, containerd.Stopped:
			// An exited task no longer needs resuming. Do not let it retain
			// a recovery marker and prevent CRI from starting.
			return nil
		case containerd.Paused:
			if err := task.Resume(ctx); err != nil {
				return fmt.Errorf("failed to resume checkpoint container %q: %w", task.ID(), err)
			}
			status, err = task.Status(ctx)
			if errdefs.IsNotFound(err) {
				return nil
			}
			if err != nil {
				return fmt.Errorf("failed to verify resumed checkpoint container %q: %w", task.ID(), err)
			}
			if status.Status != containerd.Running && status.Status != containerd.Stopped {
				return fmt.Errorf("checkpoint container %q has state %s after resume; expected %s", task.ID(), status.Status, containerd.Running)
			}
			return nil
		case containerd.Pausing:
			select {
			case <-ctx.Done():
				return fmt.Errorf("waiting for checkpoint container %q to finish pausing: %w", task.ID(), ctx.Err())
			case <-ticker.C:
			}
		default:
			return fmt.Errorf("checkpoint container %q cannot be recovered from state %s", task.ID(), status.Status)
		}
	}
}

func (c *CheckpointService) removeTemporaryCheckpointImages(ctx context.Context, containerIDs []string) error {
	cleanupCtx, cancel := context.WithTimeout(ctx, loadContainerTimeout)
	defer cancel()
	for _, containerID := range containerIDs {
		image := checkpointImageName(containerID)
		if err := c.client.ImageService().Delete(cleanupCtx, image, images.SynchronousDelete()); err != nil && !errdefs.IsNotFound(err) {
			return fmt.Errorf("failed to remove temporary checkpoint image %q during recovery: %w", image, err)
		}
	}
	return nil
}
