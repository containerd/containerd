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

	"github.com/golang/glog"
	"golang.org/x/net/context"

	runtime "k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1"

	"github.com/kubernetes-incubator/cri-containerd/pkg/metadata"
)

// RemoveContainer removes the container.
func (c *criContainerdService) RemoveContainer(ctx context.Context, r *runtime.RemoveContainerRequest) (retRes *runtime.RemoveContainerResponse, retErr error) {
	glog.V(2).Infof("RemoveContainer for %q", r.GetContainerId())
	defer func() {
		if retErr == nil {
			glog.V(2).Infof("RemoveContainer %q returns successfully", r.GetContainerId())
		}
	}()

	id := r.GetContainerId()

	// Set removing state to prevent other start/remove operations against this container
	// while it's being removed.
	if err := c.setContainerRemoving(id); err != nil {
		if !metadata.IsNotExistError(err) {
			return nil, fmt.Errorf("failed to set removing state for container %q: %v",
				id, err)
		}
		// Do not return error if container metadata doesn't exist.
		glog.V(5).Infof("RemoveContainer called for container %q that does not exist", id)
		return &runtime.RemoveContainerResponse{}, nil
	}
	defer func() {
		if retErr == nil {
			// Cleanup all index after successfully remove the container.
			c.containerNameIndex.ReleaseByKey(id)
			return
		}
		// Reset removing if remove failed.
		if err := c.resetContainerRemoving(id); err != nil {
			// TODO(random-liu): Deal with update failure. Actually Removing doesn't need to
			// be checkpointed, we only need it to have the same lifecycle with container metadata.
			glog.Errorf("failed to reset removing state for container %q: %v",
				id, err)
		}
	}()

	// NOTE(random-liu): Docker set container to "Dead" state when start removing the
	// container so as to avoid start/restart the container again. However, for current
	// kubelet implementation, we'll never start a container once we decide to remove it,
	// so we don't need the "Dead" state for now.

	// TODO(random-liu): [P0] Cleanup snapshot after switching to new snapshot api.

	// Cleanup container root directory.
	containerRootDir := getContainerRootDir(c.rootDir, id)
	if err := c.os.RemoveAll(containerRootDir); err != nil {
		return nil, fmt.Errorf("failed to remove container root directory %q: %v",
			containerRootDir, err)
	}

	// Delete container metadata.
	if err := c.containerStore.Delete(id); err != nil {
		return nil, fmt.Errorf("failed to delete container metadata for %q: %v", id, err)
	}

	return &runtime.RemoveContainerResponse{}, nil
}

// setContainerRemoving sets the container into removing state. In removing state, the
// container will not be started or removed again.
func (c *criContainerdService) setContainerRemoving(id string) error {
	return c.containerStore.Update(id, func(meta metadata.ContainerMetadata) (metadata.ContainerMetadata, error) {
		// Do not remove container if it's still running.
		if meta.State() == runtime.ContainerState_CONTAINER_RUNNING {
			return meta, fmt.Errorf("container %q is still running", id)
		}
		if meta.Removing {
			return meta, fmt.Errorf("container is already in removing state")
		}
		meta.Removing = true
		return meta, nil
	})
}

// resetContainerRemoving resets the container removing state on remove failure. So
// that we could remove the container again.
func (c *criContainerdService) resetContainerRemoving(id string) error {
	return c.containerStore.Update(id, func(meta metadata.ContainerMetadata) (metadata.ContainerMetadata, error) {
		meta.Removing = false
		return meta, nil
	})
}
