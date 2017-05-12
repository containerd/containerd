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
	"github.com/containerd/containerd/api/types/container"

	"k8s.io/kubernetes/pkg/kubelet/api/v1alpha1/runtime"

	"github.com/kubernetes-incubator/cri-containerd/pkg/metadata"
)

// PodSandboxStatus returns the status of the PodSandbox.
func (c *criContainerdService) PodSandboxStatus(ctx context.Context, r *runtime.PodSandboxStatusRequest) (retRes *runtime.PodSandboxStatusResponse, retErr error) {
	glog.V(4).Infof("PodSandboxStatus for sandbox %q", r.GetPodSandboxId())
	defer func() {
		if retErr == nil {
			glog.V(4).Infof("PodSandboxStatus returns status %+v", retRes.GetStatus())
		}
	}()

	sandbox, err := c.getSandbox(r.GetPodSandboxId())
	if err != nil {
		return nil, fmt.Errorf("failed to find sandbox %q: %v", r.GetPodSandboxId(), err)
	}
	if sandbox == nil {
		return nil, fmt.Errorf("sandbox %q does not exist", r.GetPodSandboxId())
	}
	// Use the full sandbox id.
	id := sandbox.ID

	info, err := c.containerService.Info(ctx, &execution.InfoRequest{ID: id})
	if err != nil && !isContainerdContainerNotExistError(err) {
		return nil, fmt.Errorf("failed to get sandbox container info for %q: %v", id, err)
	}

	// Set sandbox state to NOTREADY by default.
	state := runtime.PodSandboxState_SANDBOX_NOTREADY
	// If the sandbox container is running, treat it as READY.
	if info != nil && info.Status == container.Status_RUNNING {
		state = runtime.PodSandboxState_SANDBOX_READY
	}

	return &runtime.PodSandboxStatusResponse{Status: toCRISandboxStatus(sandbox, state)}, nil
}

// toCRISandboxStatus converts sandbox metadata into CRI pod sandbox status.
func toCRISandboxStatus(meta *metadata.SandboxMetadata, state runtime.PodSandboxState) *runtime.PodSandboxStatus {
	nsOpts := meta.Config.GetLinux().GetSecurityContext().GetNamespaceOptions()
	netNS := meta.NetNS
	if state == runtime.PodSandboxState_SANDBOX_NOTREADY {
		// Return empty network namespace when sandbox is not ready.
		// For kubenet, when sandbox is not running, both empty
		// network namespace and a valid permanent network namespace
		// work. Go with the first option here because it's the current
		// behavior in Kubernetes.
		netNS = ""
	}
	return &runtime.PodSandboxStatus{
		Id:        meta.ID,
		Metadata:  meta.Config.GetMetadata(),
		State:     state,
		CreatedAt: meta.CreatedAt,
		// TODO(random-liu): [P0] Get sandbox ip from network plugin.
		Network: &runtime.PodSandboxNetworkStatus{},
		Linux: &runtime.LinuxPodSandboxStatus{
			Namespaces: &runtime.Namespace{
				// TODO(random-liu): Revendor new CRI version and get
				// rid of this field.
				Network: netNS,
				Options: &runtime.NamespaceOption{
					HostNetwork: nsOpts.GetHostNetwork(),
					HostPid:     nsOpts.GetHostPid(),
					HostIpc:     nsOpts.GetHostIpc(),
				},
			},
		},
		Labels:      meta.Config.GetLabels(),
		Annotations: meta.Config.GetAnnotations(),
	}
}
