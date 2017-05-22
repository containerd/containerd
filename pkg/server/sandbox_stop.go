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
	"os"

	"github.com/golang/glog"
	"golang.org/x/net/context"

	"github.com/containerd/containerd/api/services/execution"

	"k8s.io/kubernetes/pkg/kubelet/api/v1alpha1/runtime"
)

// StopPodSandbox stops the sandbox. If there are any running containers in the
// sandbox, they should be forcibly terminated.
func (c *criContainerdService) StopPodSandbox(ctx context.Context, r *runtime.StopPodSandboxRequest) (retRes *runtime.StopPodSandboxResponse, retErr error) {
	glog.V(2).Infof("StopPodSandbox for sandbox %q", r.GetPodSandboxId())
	defer func() {
		if retErr == nil {
			glog.V(2).Info("StopPodSandbox %q returns successfully", r.GetPodSandboxId())
		}
	}()

	sandbox, err := c.getSandbox(r.GetPodSandboxId())
	if err != nil {
		return nil, fmt.Errorf("an error occurred when try to find sandbox %q: %v",
			r.GetPodSandboxId(), err)
	}
	// Use the full sandbox id.
	id := sandbox.ID

	// Teardown network for sandbox.
	_, err = c.os.Stat(sandbox.NetNS)
	if err == nil {
		if teardownErr := c.netPlugin.TearDownPod(sandbox.NetNS, sandbox.Config.GetMetadata().GetNamespace(),
			sandbox.Config.GetMetadata().GetName(), id); teardownErr != nil {
			return nil, fmt.Errorf("failed to destroy network for sandbox %q: %v", id, teardownErr)
		}
	} else if !os.IsNotExist(err) { // It's ok for sandbox.NetNS to *not* exist
		return nil, fmt.Errorf("failed to stat netns path for sandbox %q before tearing down the network: %v", id, err)
	}
	glog.V(2).Info("TearDown network for sandbox %q successfully", id)

	// TODO(random-liu): [P1] Handle sandbox container graceful deletion.
	// Delete the sandbox container from containerd.
	_, err = c.containerService.Delete(ctx, &execution.DeleteRequest{ID: id})
	if err != nil && !isContainerdContainerNotExistError(err) {
		return nil, fmt.Errorf("failed to delete sandbox container %q: %v", id, err)
	}

	// TODO(random-liu): [P2] Stop all containers inside the sandbox.
	return &runtime.StopPodSandboxResponse{}, nil
}
