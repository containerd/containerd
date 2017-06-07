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

	"github.com/golang/glog"
	"golang.org/x/net/context"
	"golang.org/x/sys/unix"

	"github.com/containerd/containerd/api/services/execution"

	runtime "k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1"

	"github.com/kubernetes-incubator/cri-containerd/pkg/metadata"
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
func (c *criContainerdService) StopContainer(ctx context.Context, r *runtime.StopContainerRequest) (retRes *runtime.StopContainerResponse, retErr error) {
	glog.V(2).Infof("StopContainer for %q with timeout %d (s)", r.GetContainerId(), r.GetTimeout())
	defer func() {
		if retErr == nil {
			glog.V(2).Infof("StopContainer %q returns successfully", r.GetContainerId())
		}
	}()

	// Get container config from container store.
	meta, err := c.containerStore.Get(r.GetContainerId())
	if err != nil {
		return nil, fmt.Errorf("an error occurred when try to find container %q: %v", r.GetContainerId(), err)
	}
	id := r.GetContainerId()

	// Return without error if container is not running. This makes sure that
	// stop only takes real action after the container is started.
	if meta.State() != runtime.ContainerState_CONTAINER_RUNNING {
		glog.V(2).Infof("Container to stop %q is not running, current state %q",
			id, criContainerStateToString(meta.State()))
		return &runtime.StopContainerResponse{}, nil
	}

	if r.GetTimeout() > 0 {
		// TODO(random-liu): [P1] Get stop signal from image config.
		stopSignal := unix.SIGTERM
		glog.V(2).Infof("Stop container %q with signal %v", id, stopSignal)
		_, err = c.containerService.Kill(ctx, &execution.KillRequest{ContainerID: id, Signal: uint32(stopSignal)})
		if err != nil {
			if !isContainerdContainerNotExistError(err) && !isRuncProcessAlreadyFinishedError(err) {
				return nil, fmt.Errorf("failed to stop container %q: %v", id, err)
			}
			// Move on to make sure container status is updated.
		}

		err = c.waitContainerStop(ctx, id, time.Duration(r.GetTimeout())*time.Second)
		if err == nil {
			return &runtime.StopContainerResponse{}, nil
		}
		glog.Errorf("Stop container %q timed out: %v", id, err)
	}

	// Event handler will Delete the container from containerd after it handles the Exited event.
	glog.V(2).Infof("Kill container %q", id)
	_, err = c.containerService.Kill(ctx, &execution.KillRequest{ContainerID: id, Signal: uint32(unix.SIGKILL)})
	if err != nil {
		if !isContainerdContainerNotExistError(err) && !isRuncProcessAlreadyFinishedError(err) {
			return nil, fmt.Errorf("failed to kill container %q: %v", id, err)
		}
		// Move on to make sure container status is updated.
	}

	// Wait for a fixed timeout until container stop is observed by event monitor.
	if err := c.waitContainerStop(ctx, id, killContainerTimeout); err != nil {
		return nil, fmt.Errorf("an error occurs during waiting for container %q to stop: %v",
			id, err)
	}
	return &runtime.StopContainerResponse{}, nil
}

// waitContainerStop polls container state until timeout exceeds or container is stopped.
func (c *criContainerdService) waitContainerStop(ctx context.Context, id string, timeout time.Duration) error {
	ticker := time.NewTicker(stopCheckPollInterval)
	defer ticker.Stop()
	timeoutTimer := time.NewTimer(timeout)
	defer timeoutTimer.Stop()
	for {
		// Poll once before waiting for stopCheckPollInterval.
		meta, err := c.containerStore.Get(id)
		if err != nil {
			if !metadata.IsNotExistError(err) {
				return fmt.Errorf("failed to get container %q metadata: %v", id, err)
			}
			// Do not return error here because container was removed means
			// it is already stopped.
			glog.Warningf("Container %q was removed during stopping", id)
			return nil
		}
		// TODO(random-liu): Use channel with event handler instead of polling.
		if meta.State() == runtime.ContainerState_CONTAINER_EXITED {
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
