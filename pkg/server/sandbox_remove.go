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

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/errdefs"
	"github.com/docker/docker/pkg/system"
	"github.com/golang/glog"
	"golang.org/x/net/context"
	"k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1/runtime"

	"github.com/kubernetes-incubator/cri-containerd/pkg/store"
)

// RemovePodSandbox removes the sandbox. If there are running containers in the
// sandbox, they should be forcibly removed.
func (c *criContainerdService) RemovePodSandbox(ctx context.Context, r *runtime.RemovePodSandboxRequest) (*runtime.RemovePodSandboxResponse, error) {
	sandbox, err := c.sandboxStore.Get(r.GetPodSandboxId())
	if err != nil {
		if err != store.ErrNotExist {
			return nil, fmt.Errorf("an error occurred when try to find sandbox %q: %v",
				r.GetPodSandboxId(), err)
		}
		// Do not return error if the id doesn't exist.
		glog.V(5).Infof("RemovePodSandbox called for sandbox %q that does not exist",
			r.GetPodSandboxId())
		return &runtime.RemovePodSandboxResponse{}, nil
	}
	// Use the full sandbox id.
	id := sandbox.ID

	// Return error if sandbox container is not fully stopped.
	// TODO(random-liu): [P0] Make sure network is torn down, may need to introduce a state.
	_, err = sandbox.Container.Task(ctx, nil)
	if err != nil && !errdefs.IsNotFound(err) {
		return nil, fmt.Errorf("failed to get sandbox container info for %q: %v", id, err)
	}
	if err == nil {
		return nil, fmt.Errorf("sandbox container %q is not fully stopped", id)
	}

	// Remove all containers inside the sandbox.
	// NOTE(random-liu): container could still be created after this point, Kubelet should
	// not rely on this behavior.
	// TODO(random-liu): Introduce an intermediate state to avoid container creation after
	// this point.
	cntrs := c.containerStore.List()
	for _, cntr := range cntrs {
		if cntr.SandboxID != id {
			continue
		}
		_, err = c.RemoveContainer(ctx, &runtime.RemoveContainerRequest{ContainerId: cntr.ID})
		if err != nil {
			return nil, fmt.Errorf("failed to remove container %q: %v", cntr.ID, err)
		}
	}

	// Cleanup the sandbox root directory.
	sandboxRootDir := getSandboxRootDir(c.config.RootDir, id)
	if err := system.EnsureRemoveAll(sandboxRootDir); err != nil {
		return nil, fmt.Errorf("failed to remove sandbox root directory %q: %v",
			sandboxRootDir, err)
	}

	// Delete sandbox container.
	if err := sandbox.Container.Delete(ctx, containerd.WithSnapshotCleanup); err != nil {
		if !errdefs.IsNotFound(err) {
			return nil, fmt.Errorf("failed to delete sandbox container %q: %v", id, err)
		}
		glog.V(5).Infof("Remove called for sandbox container %q that does not exist", id, err)
	}

	// Remove sandbox from sandbox store. Note that once the sandbox is successfully
	// deleted:
	// 1) ListPodSandbox will not include this sandbox.
	// 2) PodSandboxStatus and StopPodSandbox will return error.
	// 3) On-going operations which have held the reference will not be affected.
	c.sandboxStore.Delete(id)

	// Release the sandbox name reserved for the sandbox.
	c.sandboxNameIndex.ReleaseByKey(id)

	return &runtime.RemovePodSandboxResponse{}, nil
}
