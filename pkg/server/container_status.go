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

	"golang.org/x/net/context"
	"k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1/runtime"

	containerstore "github.com/kubernetes-incubator/cri-containerd/pkg/store/container"
)

// ContainerStatus inspects the container and returns the status.
func (c *criContainerdService) ContainerStatus(ctx context.Context, r *runtime.ContainerStatusRequest) (*runtime.ContainerStatusResponse, error) {
	container, err := c.containerStore.Get(r.GetContainerId())
	if err != nil {
		return nil, fmt.Errorf("an error occurred when try to find container %q: %v", r.GetContainerId(), err)
	}

	imageRef := container.ImageRef
	image, err := c.imageStore.Get(imageRef)
	if err != nil {
		return nil, fmt.Errorf("failed to get image %q: %v", container.ImageRef, err)
	}
	if len(image.RepoDigests) > 0 {
		// Based on the CRI definition, this field will be consumed by user.
		imageRef = image.RepoDigests[0]
	}

	return &runtime.ContainerStatusResponse{
		Status: toCRIContainerStatus(container, imageRef),
	}, nil
}

// toCRIContainerStatus converts internal container object to CRI container status.
func toCRIContainerStatus(container containerstore.Container, imageRef string) *runtime.ContainerStatus {
	meta := container.Metadata
	status := container.Status.Get()
	reason := status.Reason
	if status.State() == runtime.ContainerState_CONTAINER_EXITED && reason == "" {
		if status.ExitCode == 0 {
			reason = completeExitReason
		} else {
			reason = errorExitReason
		}
	}
	return &runtime.ContainerStatus{
		Id:          meta.ID,
		Metadata:    meta.Config.GetMetadata(),
		State:       status.State(),
		CreatedAt:   status.CreatedAt,
		StartedAt:   status.StartedAt,
		FinishedAt:  status.FinishedAt,
		ExitCode:    status.ExitCode,
		Image:       meta.Config.GetImage(),
		ImageRef:    imageRef,
		Reason:      reason,
		Message:     status.Message,
		Labels:      meta.Config.GetLabels(),
		Annotations: meta.Config.GetAnnotations(),
		Mounts:      meta.Config.GetMounts(),
	}
}
