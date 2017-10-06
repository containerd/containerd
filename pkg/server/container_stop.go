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
	"time"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/errdefs"
	"github.com/docker/docker/pkg/signal"
	"github.com/golang/glog"
	"golang.org/x/net/context"
	"golang.org/x/sys/unix"
	"k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1/runtime"

	"github.com/kubernetes-incubator/cri-containerd/pkg/store"
	containerstore "github.com/kubernetes-incubator/cri-containerd/pkg/store/container"
)

const (
	// stopCheckPollInterval is the the interval to check whether a container
	// is stopped successfully.
	stopCheckPollInterval = 100 * time.Millisecond

	// killContainerTimeout is the timeout that we wait for the container to
	// be SIGKILLed.
	killContainerTimeout = 2 * time.Minute
)

// StopContainer stops a running container with a grace period (i.e., timeout).
func (c *criContainerdService) StopContainer(ctx context.Context, r *runtime.StopContainerRequest) (*runtime.StopContainerResponse, error) {
	// Get container config from container store.
	container, err := c.containerStore.Get(r.GetContainerId())
	if err != nil {
		return nil, fmt.Errorf("an error occurred when try to find container %q: %v", r.GetContainerId(), err)
	}

	if err := c.stopContainer(ctx, container, time.Duration(r.GetTimeout())*time.Second); err != nil {
		return nil, err
	}

	return &runtime.StopContainerResponse{}, nil
}

// stopContainer stops a container based on the container metadata.
func (c *criContainerdService) stopContainer(ctx context.Context, container containerstore.Container, timeout time.Duration) error {
	id := container.ID

	// Return without error if container is not running. This makes sure that
	// stop only takes real action after the container is started.
	state := container.Status.Get().State()
	if state != runtime.ContainerState_CONTAINER_RUNNING {
		glog.V(2).Infof("Container to stop %q is not running, current state %q",
			id, criContainerStateToString(state))
		return nil
	}

	if timeout > 0 {
		stopSignal := unix.SIGTERM
		image, err := c.imageStore.Get(container.ImageRef)
		if err != nil {
			// NOTE(random-liu): It's possible that the container is stopped,
			// deleted and image is garbage collected before this point. However,
			// the chance is really slim, even it happens, it's still fine to return
			// an error here.
			return fmt.Errorf("failed to get image metadata %q: %v", container.ImageRef, err)
		}
		if image.Config.StopSignal != "" {
			stopSignal, err = signal.ParseSignal(image.Config.StopSignal)
			if err != nil {
				return fmt.Errorf("failed to parse stop signal %q: %v",
					image.Config.StopSignal, err)
			}
		}
		glog.V(2).Infof("Stop container %q with signal %v", id, stopSignal)
		task, err := container.Container.Task(ctx, nil)
		if err != nil {
			if !errdefs.IsNotFound(err) {
				return fmt.Errorf("failed to stop container, task not found for container %q: %v", id, err)
			}
			return nil
		}
		if task != nil {
			if err = task.Kill(ctx, stopSignal); err != nil {
				if !errdefs.IsNotFound(err) {
					return fmt.Errorf("failed to stop container %q: %v", id, err)
				}
				// Move on to make sure container status is updated.
			}
		}

		err = c.waitContainerStop(ctx, id, timeout)
		if err == nil {
			return nil
		}
		glog.Errorf("Stop container %q timed out: %v", id, err)
	}

	task, err := container.Container.Task(ctx, nil)
	if err != nil {
		if !errdefs.IsNotFound(err) {
			return fmt.Errorf("failed to stop container, task not found for container %q: %v", id, err)
		}
		return nil
	}
	// Event handler will Delete the container from containerd after it handles the Exited event.
	glog.V(2).Infof("Kill container %q", id)
	if task != nil {
		if err = task.Kill(ctx, unix.SIGKILL, containerd.WithKillAll); err != nil {
			if !errdefs.IsNotFound(err) {
				return fmt.Errorf("failed to kill container %q: %v", id, err)
			}
			// Move on to make sure container status is updated.
		}
	}

	// Wait for a fixed timeout until container stop is observed by event monitor.
	if err := c.waitContainerStop(ctx, id, killContainerTimeout); err != nil {
		return fmt.Errorf("an error occurs during waiting for container %q to stop: %v", id, err)
	}
	return nil
}

// waitContainerStop polls container state until timeout exceeds or container is stopped.
func (c *criContainerdService) waitContainerStop(ctx context.Context, id string, timeout time.Duration) error {
	ticker := time.NewTicker(stopCheckPollInterval)
	defer ticker.Stop()
	timeoutTimer := time.NewTimer(timeout)
	defer timeoutTimer.Stop()
	for {
		// Poll once before waiting for stopCheckPollInterval.
		container, err := c.containerStore.Get(id)
		if err != nil {
			if err != store.ErrNotExist {
				return fmt.Errorf("failed to get container %q: %v", id, err)
			}
			// Do not return error here because container was removed means
			// it is already stopped.
			glog.Warningf("Container %q was removed during stopping", id)
			return nil
		}
		// TODO(random-liu): Use channel with event handler instead of polling.
		if container.Status.Get().State() == runtime.ContainerState_CONTAINER_EXITED {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait container %q is cancelled", id)
		case <-timeoutTimer.C:
			return fmt.Errorf("wait container %q stop timeout", id)
		case <-ticker.C:
			continue
		}
	}
}
