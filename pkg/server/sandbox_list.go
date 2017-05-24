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
	runtime "k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1"

	"github.com/kubernetes-incubator/cri-containerd/pkg/metadata"
)

// ListPodSandbox returns a list of Sandbox.
func (c *criContainerdService) ListPodSandbox(ctx context.Context, r *runtime.ListPodSandboxRequest) (retRes *runtime.ListPodSandboxResponse, retErr error) {
	glog.V(4).Infof("ListPodSandbox with filter %+v", r.GetFilter())
	defer func() {
		if retErr == nil {
			glog.V(4).Infof("ListPodSandbox returns sandboxes %+v", retRes.GetItems())
		}
	}()

	// List all sandbox metadata from store.
	sandboxesInStore, err := c.sandboxStore.List()
	if err != nil {
		return nil, fmt.Errorf("failed to list metadata from sandbox store: %v", err)
	}

	resp, err := c.containerService.List(ctx, &execution.ListRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to list sandbox containers: %v", err)
	}
	sandboxesInContainerd := resp.Containers

	var sandboxes []*runtime.PodSandbox
	for _, sandboxInStore := range sandboxesInStore {
		var sandboxInContainerd *container.Container
		for _, s := range sandboxesInContainerd {
			if s.ID == sandboxInStore.ID {
				sandboxInContainerd = s
				break
			}
		}

		// Set sandbox state to NOTREADY by default.
		state := runtime.PodSandboxState_SANDBOX_NOTREADY
		// If the sandbox container is running, return the sandbox as READY.
		if sandboxInContainerd != nil && sandboxInContainerd.Status == container.Status_RUNNING {
			state = runtime.PodSandboxState_SANDBOX_READY
		}

		sandboxes = append(sandboxes, toCRISandbox(sandboxInStore, state))
	}

	sandboxes = c.filterCRISandboxes(sandboxes, r.GetFilter())
	return &runtime.ListPodSandboxResponse{Items: sandboxes}, nil
}

// toCRISandbox converts sandbox metadata into CRI pod sandbox.
func toCRISandbox(meta *metadata.SandboxMetadata, state runtime.PodSandboxState) *runtime.PodSandbox {
	return &runtime.PodSandbox{
		Id:          meta.ID,
		Metadata:    meta.Config.GetMetadata(),
		State:       state,
		CreatedAt:   meta.CreatedAt,
		Labels:      meta.Config.GetLabels(),
		Annotations: meta.Config.GetAnnotations(),
	}
}

// filterCRISandboxes filters CRISandboxes.
func (c *criContainerdService) filterCRISandboxes(sandboxes []*runtime.PodSandbox, filter *runtime.PodSandboxFilter) []*runtime.PodSandbox {
	if filter == nil {
		return sandboxes
	}

	var filterID string
	if filter.GetId() != "" {
		// Handle truncate id. Use original filter if failed to convert.
		var err error
		filterID, err = c.sandboxIDIndex.Get(filter.GetId())
		if err != nil {
			filterID = filter.GetId()
		}
	}

	filtered := []*runtime.PodSandbox{}
	for _, s := range sandboxes {
		// Filter by id
		if filterID != "" && filterID != s.Id {
			continue
		}
		// Filter by state
		if filter.GetState() != nil && filter.GetState().GetState() != s.State {
			continue
		}
		// Filter by label
		if filter.GetLabelSelector() != nil {
			match := true
			for k, v := range filter.GetLabelSelector() {
				got, ok := s.Labels[k]
				if !ok || got != v {
					match = false
					break
				}
			}
			if !match {
				continue
			}
		}
		filtered = append(filtered, s)
	}

	return filtered
}
