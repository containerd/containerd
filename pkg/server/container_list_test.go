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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/context"

	"k8s.io/kubernetes/pkg/kubelet/api/v1alpha1/runtime"

	"github.com/kubernetes-incubator/cri-containerd/pkg/metadata"
)

func TestToCRIContainer(t *testing.T) {
	config := &runtime.ContainerConfig{
		Metadata: &runtime.ContainerMetadata{
			Name:    "test-name",
			Attempt: 1,
		},
		Image:       &runtime.ImageSpec{Image: "test-image"},
		Labels:      map[string]string{"a": "b"},
		Annotations: map[string]string{"c": "d"},
	}
	createdAt := time.Now().UnixNano()
	meta := &metadata.ContainerMetadata{
		ID:         "test-id",
		Name:       "test-name",
		SandboxID:  "test-sandbox-id",
		Config:     config,
		ImageRef:   "test-image-ref",
		Pid:        1234,
		CreatedAt:  createdAt,
		StartedAt:  time.Now().UnixNano(),
		FinishedAt: time.Now().UnixNano(),
		ExitCode:   1,
		Reason:     "test-reason",
		Message:    "test-message",
	}
	expect := &runtime.Container{
		Id:           "test-id",
		PodSandboxId: "test-sandbox-id",
		Metadata:     config.GetMetadata(),
		Image:        config.GetImage(),
		ImageRef:     "test-image-ref",
		State:        runtime.ContainerState_CONTAINER_EXITED,
		CreatedAt:    createdAt,
		Labels:       config.GetLabels(),
		Annotations:  config.GetAnnotations(),
	}
	c := toCRIContainer(meta)
	assert.Equal(t, expect, c)
}

func TestFilterContainers(t *testing.T) {
	c := newTestCRIContainerdService()

	testContainers := []*runtime.Container{
		{
			Id:           "1",
			PodSandboxId: "s-1",
			Metadata:     &runtime.ContainerMetadata{Name: "name-1", Attempt: 1},
			State:        runtime.ContainerState_CONTAINER_RUNNING,
		},
		{
			Id:           "2",
			PodSandboxId: "s-2",
			Metadata:     &runtime.ContainerMetadata{Name: "name-2", Attempt: 2},
			State:        runtime.ContainerState_CONTAINER_EXITED,
			Labels:       map[string]string{"a": "b"},
		},
		{
			Id:           "3",
			PodSandboxId: "s-2",
			Metadata:     &runtime.ContainerMetadata{Name: "name-2", Attempt: 3},
			State:        runtime.ContainerState_CONTAINER_CREATED,
			Labels:       map[string]string{"c": "d"},
		},
	}
	for desc, test := range map[string]struct {
		filter *runtime.ContainerFilter
		expect []*runtime.Container
	}{
		"no filter": {
			expect: testContainers,
		},
		"id filter": {
			filter: &runtime.ContainerFilter{Id: "2"},
			expect: []*runtime.Container{testContainers[1]},
		},
		"state filter": {
			filter: &runtime.ContainerFilter{
				State: &runtime.ContainerStateValue{
					State: runtime.ContainerState_CONTAINER_EXITED,
				},
			},
			expect: []*runtime.Container{testContainers[1]},
		},
		"label filter": {
			filter: &runtime.ContainerFilter{
				LabelSelector: map[string]string{"a": "b"},
			},
			expect: []*runtime.Container{testContainers[1]},
		},
		"sandbox id filter": {
			filter: &runtime.ContainerFilter{PodSandboxId: "s-2"},
			expect: []*runtime.Container{testContainers[1], testContainers[2]},
		},
		"mixed filter not matched": {
			filter: &runtime.ContainerFilter{
				Id:            "1",
				PodSandboxId:  "s-2",
				LabelSelector: map[string]string{"a": "b"},
			},
			expect: []*runtime.Container{},
		},
		"mixed filter matched": {
			filter: &runtime.ContainerFilter{
				PodSandboxId: "s-2",
				State: &runtime.ContainerStateValue{
					State: runtime.ContainerState_CONTAINER_CREATED,
				},
				LabelSelector: map[string]string{"c": "d"},
			},
			expect: []*runtime.Container{testContainers[2]},
		},
	} {
		filtered := c.filterCRIContainers(testContainers, test.filter)
		assert.Equal(t, test.expect, filtered, desc)
	}
}

func TestListContainers(t *testing.T) {
	c := newTestCRIContainerdService()

	createdAt := time.Now().UnixNano()
	startedAt := time.Now().UnixNano()
	finishedAt := time.Now().UnixNano()
	containersInStore := []metadata.ContainerMetadata{
		{
			ID:        "1",
			Name:      "name-1",
			SandboxID: "s-1",
			Config:    &runtime.ContainerConfig{Metadata: &runtime.ContainerMetadata{Name: "name-1"}},
			CreatedAt: createdAt,
		},
		{
			ID:        "2",
			Name:      "name-2",
			SandboxID: "s-1",
			Config:    &runtime.ContainerConfig{Metadata: &runtime.ContainerMetadata{Name: "name-2"}},
			CreatedAt: createdAt,
			StartedAt: startedAt,
		},
		{
			ID:         "3",
			Name:       "name-3",
			SandboxID:  "s-1",
			Config:     &runtime.ContainerConfig{Metadata: &runtime.ContainerMetadata{Name: "name-3"}},
			CreatedAt:  createdAt,
			StartedAt:  startedAt,
			FinishedAt: finishedAt,
		},
		{
			ID:        "4",
			Name:      "name-4",
			SandboxID: "s-2",
			Config:    &runtime.ContainerConfig{Metadata: &runtime.ContainerMetadata{Name: "name-4"}},
			CreatedAt: createdAt,
		},
	}
	filter := &runtime.ContainerFilter{
		PodSandboxId: "s-1",
	}
	expect := []*runtime.Container{
		{
			Id:           "1",
			PodSandboxId: "s-1",
			Metadata:     &runtime.ContainerMetadata{Name: "name-1"},
			State:        runtime.ContainerState_CONTAINER_CREATED,
			CreatedAt:    createdAt,
		},
		{
			Id:           "2",
			PodSandboxId: "s-1",
			Metadata:     &runtime.ContainerMetadata{Name: "name-2"},
			State:        runtime.ContainerState_CONTAINER_RUNNING,
			CreatedAt:    createdAt,
		},
		{
			Id:           "3",
			PodSandboxId: "s-1",
			Metadata:     &runtime.ContainerMetadata{Name: "name-3"},
			State:        runtime.ContainerState_CONTAINER_EXITED,
			CreatedAt:    createdAt,
		},
	}

	// Inject test metadata
	for _, cntr := range containersInStore {
		c.containerStore.Create(cntr)
	}

	resp, err := c.ListContainers(context.Background(), &runtime.ListContainersRequest{Filter: filter})
	assert.NoError(t, err)
	require.NotNil(t, resp)
	containers := resp.GetContainers()
	assert.Len(t, containers, len(expect))
	for _, cntr := range expect {
		assert.Contains(t, containers, cntr)
	}
}
