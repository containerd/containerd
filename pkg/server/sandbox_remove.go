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

	"github.com/containerd/containerd/api/services/execution"

	runtime "k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1"

	"github.com/kubernetes-incubator/cri-containerd/pkg/metadata"
)

// RemovePodSandbox removes the sandbox. If there are running containers in the
// sandbox, they should be forcibly removed.
func (c *criContainerdService) RemovePodSandbox(ctx context.Context, r *runtime.RemovePodSandboxRequest) (retRes *runtime.RemovePodSandboxResponse, retErr error) {
	glog.V(2).Infof("RemovePodSandbox for sandbox %q", r.GetPodSandboxId())
	defer func() {
		if retErr == nil {
			glog.V(2).Info("RemovePodSandbox %q returns successfully", r.GetPodSandboxId())
		}
	}()

	sandbox, err := c.getSandbox(r.GetPodSandboxId())
	if err != nil {
		if !metadata.IsNotExistError(err) {
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

	// TODO(random-liu): [P2] Remove all containers in the sandbox.

	// Return error if sandbox container is not fully stopped.
	// TODO(random-liu): [P0] Make sure network is torn down, may need to introduce a state.
	_, err = c.containerService.Info(ctx, &execution.InfoRequest{ID: id})
	if err != nil && !isContainerdContainerNotExistError(err) {
		return nil, fmt.Errorf("failed to get sandbox container info for %q: %v", id, err)
	}
	if err == nil {
		return nil, fmt.Errorf("sandbox container %q is not fully stopped", id)
	}

	// TODO(random-liu): [P0] Cleanup shm created in RunPodSandbox.
	// TODO(random-liu): [P1] Remove permanent namespace once used.

	// Cleanup the sandbox root directory.
	sandboxRootDir := getSandboxRootDir(c.rootDir, id)
	if err := c.os.RemoveAll(sandboxRootDir); err != nil {
		return nil, fmt.Errorf("failed to remove sandbox root directory %q: %v",
			sandboxRootDir, err)
	}

	// Remove sandbox metadata from metadata store. Note that once the sandbox
	// metadata is successfully deleted:
	// 1) ListPodSandbox will not include this sandbox.
	// 2) PodSandboxStatus and StopPodSandbox will return error.
	// 3) On-going operations which have held the metadata reference will not be
	// affected.
	if err := c.sandboxStore.Delete(id); err != nil {
		return nil, fmt.Errorf("failed to delete sandbox metadata for %q: %v", id, err)
	}

	// Release the sandbox id from id index.
	c.sandboxIDIndex.Delete(id) // nolint: errcheck

	// Release the sandbox name reserved for the sandbox.
	c.sandboxNameIndex.ReleaseByKey(id)

	return &runtime.RemovePodSandboxResponse{}, nil
}
