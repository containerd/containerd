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

	tasks "github.com/containerd/containerd/api/services/tasks/v1"
	"github.com/containerd/containerd/api/types/task"
	"github.com/containerd/containerd/errdefs"
	"golang.org/x/net/context"
	"k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1/runtime"

	sandboxstore "github.com/kubernetes-incubator/cri-containerd/pkg/store/sandbox"
)

// ListPodSandbox returns a list of Sandbox.
func (c *criContainerdService) ListPodSandbox(ctx context.Context, r *runtime.ListPodSandboxRequest) (*runtime.ListPodSandboxResponse, error) {
	// List all sandboxes from store.
	sandboxesInStore := c.sandboxStore.List()

	response, err := c.taskService.List(ctx, &tasks.ListTasksRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to list sandbox containers: %v", err)
	}

	var sandboxes []*runtime.PodSandbox
	for _, sandboxInStore := range sandboxesInStore {
		var sandboxInContainerd *task.Process
		for _, s := range response.Tasks {
			if s.ID == sandboxInStore.ID {
				sandboxInContainerd = s
				break
			}
		}

		// Set sandbox state to NOTREADY by default.
		state := runtime.PodSandboxState_SANDBOX_NOTREADY
		// If the sandbox container is running, return the sandbox as READY.
		if sandboxInContainerd != nil && sandboxInContainerd.Status == task.StatusRunning {
			state = runtime.PodSandboxState_SANDBOX_READY
		}

		info, err := sandboxInStore.Container.Info(ctx)
		if err != nil {
			// It's possible that container gets deleted during list.
			if errdefs.IsNotFound(err) {
				continue
			}
			return nil, fmt.Errorf("failed to get sandbox container %q info: %v", sandboxInStore.ID, err)
		}
		createdAt := info.CreatedAt
		sandboxes = append(sandboxes, toCRISandbox(sandboxInStore.Metadata, state, createdAt))
	}

	sandboxes = c.filterCRISandboxes(sandboxes, r.GetFilter())
	return &runtime.ListPodSandboxResponse{Items: sandboxes}, nil
}

// toCRISandbox converts sandbox metadata into CRI pod sandbox.
func toCRISandbox(meta sandboxstore.Metadata, state runtime.PodSandboxState, createdAt time.Time) *runtime.PodSandbox {
	return &runtime.PodSandbox{
		Id:          meta.ID,
		Metadata:    meta.Config.GetMetadata(),
		State:       state,
		CreatedAt:   createdAt.UnixNano(),
		Labels:      meta.Config.GetLabels(),
		Annotations: meta.Config.GetAnnotations(),
	}
}

func (c *criContainerdService) normalizePodSandboxFilter(filter *runtime.PodSandboxFilter) {
	if sb, err := c.sandboxStore.Get(filter.GetId()); err == nil {
		filter.Id = sb.ID
	}
}

// filterCRISandboxes filters CRISandboxes.
func (c *criContainerdService) filterCRISandboxes(sandboxes []*runtime.PodSandbox, filter *runtime.PodSandboxFilter) []*runtime.PodSandbox {
	if filter == nil {
		return sandboxes
	}

	c.normalizePodSandboxFilter(filter)
	filtered := []*runtime.PodSandbox{}
	for _, s := range sandboxes {
		// Filter by id
		if filter.GetId() != "" && filter.GetId() != s.Id {
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
