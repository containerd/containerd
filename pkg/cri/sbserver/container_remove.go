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

package sbserver

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/log"
	containerstore "github.com/containerd/containerd/pkg/cri/store/container"
	"github.com/sirupsen/logrus"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// RemoveContainer removes the container.
func (c *criService) RemoveContainer(ctx context.Context, r *runtime.RemoveContainerRequest) (_ *runtime.RemoveContainerResponse, retErr error) {
	start := time.Now()
	container, err := c.containerStore.Get(r.GetContainerId())
	if err != nil {
		if !errdefs.IsNotFound(err) {
			return nil, fmt.Errorf("an error occurred when try to find container %q: %w", r.GetContainerId(), err)
		}
		// Do not return error if container metadata doesn't exist.
		log.G(ctx).Tracef("RemoveContainer called for container %q that does not exist", r.GetContainerId())
		return &runtime.RemoveContainerResponse{}, nil
	}
	id := container.ID
	i, err := container.Container.Info(ctx)
	if err != nil {
		return nil, fmt.Errorf("get container info: %w", err)
	}

	// Forcibly stop the containers if they are in running or unknown state
	state := container.Status.Get().State()
	if state == runtime.ContainerState_CONTAINER_RUNNING ||
		state == runtime.ContainerState_CONTAINER_UNKNOWN {
		logrus.Infof("Forcibly stopping container %q", id)
		if err := c.stopContainer(ctx, container, 0); err != nil {
			return nil, fmt.Errorf("failed to forcibly stop container %q: %w", id, err)
		}

	}

	// Set removing state to prevent other start/remove operations against this container
	// while it's being removed.
	if err := setContainerRemoving(container); err != nil {
		return nil, fmt.Errorf("failed to set removing state for container %q: %w", id, err)
	}
	defer func() {
		if retErr != nil {
			// Reset removing if remove failed.
			if err := resetContainerRemoving(container); err != nil {
				log.G(ctx).WithError(err).Errorf("failed to reset removing state for container %q", id)
			}
		}
	}()

	sandbox, err := c.sandboxStore.Get(container.SandboxID)
	if err != nil {
		err = c.nri.RemoveContainer(ctx, nil, &container)
	} else {
		err = c.nri.RemoveContainer(ctx, &sandbox, &container)
	}
	if err != nil {
		log.G(ctx).WithError(err).Error("NRI failed to remove container")
	}

	// NOTE(random-liu): Docker set container to "Dead" state when start removing the
	// container so as to avoid start/restart the container again. However, for current
	// kubelet implementation, we'll never start a container once we decide to remove it,
	// so we don't need the "Dead" state for now.

	// Delete containerd container.
	if err := container.Container.Delete(ctx, containerd.WithSnapshotCleanup); err != nil {
		if !errdefs.IsNotFound(err) {
			return nil, fmt.Errorf("failed to delete containerd container %q: %w", id, err)
		}
		log.G(ctx).Tracef("Remove called for containerd container %q that does not exist", id)
	}

	// Delete container checkpoint.
	if err := container.Delete(); err != nil {
		return nil, fmt.Errorf("failed to delete container checkpoint for %q: %w", id, err)
	}

	containerRootDir := c.getContainerRootDir(id)
	if err := ensureRemoveAll(ctx, containerRootDir); err != nil {
		return nil, fmt.Errorf("failed to remove container root directory %q: %w",
			containerRootDir, err)
	}
	volatileContainerRootDir := c.getVolatileContainerRootDir(id)
	if err := ensureRemoveAll(ctx, volatileContainerRootDir); err != nil {
		return nil, fmt.Errorf("failed to remove volatile container root directory %q: %w",
			volatileContainerRootDir, err)
	}

	c.containerStore.Delete(id)

	c.containerNameIndex.ReleaseByKey(id)

	c.generateAndSendContainerEvent(ctx, id, container.SandboxID, runtime.ContainerEventType_CONTAINER_DELETED_EVENT)

	containerRemoveTimer.WithValues(i.Runtime.Name).UpdateSince(start)

	return &runtime.RemoveContainerResponse{}, nil
}

// setContainerRemoving sets the container into removing state. In removing state, the
// container will not be started or removed again.
func setContainerRemoving(container containerstore.Container) error {
	return container.Status.Update(func(status containerstore.Status) (containerstore.Status, error) {
		// Do not remove container if it's still running or unknown.
		if status.State() == runtime.ContainerState_CONTAINER_RUNNING {
			return status, errors.New("container is still running, to stop first")
		}
		if status.State() == runtime.ContainerState_CONTAINER_UNKNOWN {
			return status, errors.New("container state is unknown, to stop first")
		}
		if status.Starting {
			return status, errors.New("container is in starting state, can't be removed")
		}
		if status.Removing {
			return status, errors.New("container is already in removing state")
		}
		status.Removing = true
		return status, nil
	})
}

// resetContainerRemoving resets the container removing state on remove failure. So
// that we could remove the container again.
func resetContainerRemoving(container containerstore.Container) error {
	return container.Status.Update(func(status containerstore.Status) (containerstore.Status, error) {
		status.Removing = false
		return status, nil
	})
}
