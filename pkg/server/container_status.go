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

	"k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1/runtime"

	"github.com/kubernetes-incubator/cri-containerd/pkg/metadata"
)

// ContainerStatus inspects the container and returns the status.
func (c *criContainerdService) ContainerStatus(ctx context.Context, r *runtime.ContainerStatusRequest) (retRes *runtime.ContainerStatusResponse, retErr error) {
	glog.V(4).Infof("ContainerStatus for container %q", r.GetContainerId())
	defer func() {
		if retErr == nil {
			glog.V(4).Infof("ContainerStatus for %q returns status %+v", r.GetContainerId(), retRes.GetStatus())
		}
	}()

	meta, err := c.containerStore.Get(r.GetContainerId())
	if err != nil {
		return nil, fmt.Errorf("an error occurred when try to find container %q: %v", r.GetContainerId(), err)
	}

	return &runtime.ContainerStatusResponse{
		Status: toCRIContainerStatus(meta),
	}, nil
}

// toCRIContainerStatus converts container metadata to CRI container status.
func toCRIContainerStatus(meta *metadata.ContainerMetadata) *runtime.ContainerStatus {
	state := meta.State()
	reason := meta.Reason
	if state == runtime.ContainerState_CONTAINER_EXITED && reason == "" {
		if meta.ExitCode == 0 {
			reason = completeExitReason
		} else {
			reason = errorExitReason
		}
	}
	return &runtime.ContainerStatus{
		Id:          meta.ID,
		Metadata:    meta.Config.GetMetadata(),
		State:       state,
		CreatedAt:   meta.CreatedAt,
		StartedAt:   meta.StartedAt,
		FinishedAt:  meta.FinishedAt,
		ExitCode:    meta.ExitCode,
		Image:       meta.Config.GetImage(),
		ImageRef:    meta.ImageRef,
		Reason:      reason,
		Message:     meta.Message,
		Labels:      meta.Config.GetLabels(),
		Annotations: meta.Config.GetAnnotations(),
		Mounts:      meta.Config.GetMounts(),
	}
}
