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

// ListContainers lists all containers matching the filter.
func (c *criContainerdService) ListContainers(ctx context.Context, r *runtime.ListContainersRequest) (retRes *runtime.ListContainersResponse, retErr error) {
	glog.V(4).Infof("ListContainers with filter %+v", r.GetFilter())
	defer func() {
		if retErr == nil {
			glog.V(4).Infof("ListContainers returns containers %+v", retRes.GetContainers())
		}
	}()

	// List all container metadata from store.
	metas, err := c.containerStore.List()
	if err != nil {
		return nil, fmt.Errorf("failed to list metadata from container store: %v", err)
	}

	var containers []*runtime.Container
	for _, meta := range metas {
		containers = append(containers, toCRIContainer(meta))
	}

	containers = c.filterCRIContainers(containers, r.GetFilter())
	return &runtime.ListContainersResponse{Containers: containers}, nil
}

// toCRIContainer converts container metadata into CRI container.
func toCRIContainer(meta *metadata.ContainerMetadata) *runtime.Container {
	return &runtime.Container{
		Id:           meta.ID,
		PodSandboxId: meta.SandboxID,
		Metadata:     meta.Config.GetMetadata(),
		Image:        meta.Config.GetImage(),
		ImageRef:     meta.ImageRef,
		State:        meta.State(),
		CreatedAt:    meta.CreatedAt,
		Labels:       meta.Config.GetLabels(),
		Annotations:  meta.Config.GetAnnotations(),
	}
}

// filterCRIContainers filters CRIContainers.
func (c *criContainerdService) filterCRIContainers(containers []*runtime.Container, filter *runtime.ContainerFilter) []*runtime.Container {
	if filter == nil {
		return containers
	}

	filtered := []*runtime.Container{}
	for _, cntr := range containers {
		if filter.GetId() != "" && filter.GetId() != cntr.Id {
			continue
		}
		if filter.GetPodSandboxId() != "" && filter.GetPodSandboxId() != cntr.PodSandboxId {
			continue
		}
		if filter.GetState() != nil && filter.GetState().GetState() != cntr.State {
			continue
		}
		if filter.GetLabelSelector() != nil {
			match := true
			for k, v := range filter.GetLabelSelector() {
				got, ok := cntr.Labels[k]
				if !ok || got != v {
					match = false
					break
				}
			}
			if !match {
				continue
			}
		}
		filtered = append(filtered, cntr)
	}

	return filtered
}
