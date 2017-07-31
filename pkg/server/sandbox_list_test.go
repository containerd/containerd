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
	"golang.org/x/net/context"

	"github.com/containerd/containerd/api/types/task"

	"k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1/runtime"

	servertesting "github.com/kubernetes-incubator/cri-containerd/pkg/server/testing"
	sandboxstore "github.com/kubernetes-incubator/cri-containerd/pkg/store/sandbox"
)

func TestToCRISandbox(t *testing.T) {
	config := &runtime.PodSandboxConfig{
		Metadata: &runtime.PodSandboxMetadata{
			Name:      "test-name",
			Uid:       "test-uid",
			Namespace: "test-ns",
			Attempt:   1,
		},
		Labels:      map[string]string{"a": "b"},
		Annotations: map[string]string{"c": "d"},
	}
	createdAt := time.Now().UnixNano()
	meta := sandboxstore.Metadata{
		ID:        "test-id",
		Name:      "test-name",
		Config:    config,
		CreatedAt: createdAt,
		NetNS:     "test-netns",
	}
	state := runtime.PodSandboxState_SANDBOX_READY
	expect := &runtime.PodSandbox{
		Id:          "test-id",
		Metadata:    config.GetMetadata(),
		State:       state,
		CreatedAt:   createdAt,
		Labels:      config.GetLabels(),
		Annotations: config.GetAnnotations(),
	}
	s := toCRISandbox(meta, state)
	assert.Equal(t, expect, s)
}

func TestFilterSandboxes(t *testing.T) {
	c := newTestCRIContainerdService()

	testSandboxes := []*runtime.PodSandbox{
		{
			Id:       "1",
			Metadata: &runtime.PodSandboxMetadata{Name: "name-1", Uid: "uid-1", Namespace: "ns-1", Attempt: 1},
			State:    runtime.PodSandboxState_SANDBOX_READY,
		},
		{
			Id:       "2",
			Metadata: &runtime.PodSandboxMetadata{Name: "name-2", Uid: "uid-2", Namespace: "ns-2", Attempt: 2},
			State:    runtime.PodSandboxState_SANDBOX_NOTREADY,
			Labels:   map[string]string{"a": "b"},
		},
		{
			Id:       "3",
			Metadata: &runtime.PodSandboxMetadata{Name: "name-2", Uid: "uid-2", Namespace: "ns-2", Attempt: 2},
			State:    runtime.PodSandboxState_SANDBOX_READY,
			Labels:   map[string]string{"c": "d"},
		},
	}
	for desc, test := range map[string]struct {
		filter *runtime.PodSandboxFilter
		expect []*runtime.PodSandbox
	}{
		"no filter": {
			expect: testSandboxes,
		},
		"id filter": {
			filter: &runtime.PodSandboxFilter{Id: "2"},
			expect: []*runtime.PodSandbox{testSandboxes[1]},
		},
		"state filter": {
			filter: &runtime.PodSandboxFilter{
				State: &runtime.PodSandboxStateValue{
					State: runtime.PodSandboxState_SANDBOX_READY,
				},
			},
			expect: []*runtime.PodSandbox{testSandboxes[0], testSandboxes[2]},
		},
		"label filter": {
			filter: &runtime.PodSandboxFilter{
				LabelSelector: map[string]string{"a": "b"},
			},
			expect: []*runtime.PodSandbox{testSandboxes[1]},
		},
		"mixed filter not matched": {
			filter: &runtime.PodSandboxFilter{
				Id:            "1",
				LabelSelector: map[string]string{"a": "b"},
			},
			expect: []*runtime.PodSandbox{},
		},
		"mixed filter matched": {
			filter: &runtime.PodSandboxFilter{
				State: &runtime.PodSandboxStateValue{
					State: runtime.PodSandboxState_SANDBOX_READY,
				},
				LabelSelector: map[string]string{"c": "d"},
			},
			expect: []*runtime.PodSandbox{testSandboxes[2]},
		},
	} {
		filtered := c.filterCRISandboxes(testSandboxes, test.filter)
		assert.Equal(t, test.expect, filtered, desc)
	}
}

func TestListPodSandbox(t *testing.T) {
	c := newTestCRIContainerdService()

	fake := c.taskService.(*servertesting.FakeExecutionClient)

	sandboxesInStore := []sandboxstore.Sandbox{
		{
			Metadata: sandboxstore.Metadata{
				ID:     "1",
				Name:   "name-1",
				Config: &runtime.PodSandboxConfig{Metadata: &runtime.PodSandboxMetadata{Name: "name-1"}},
			},
		},
		{
			Metadata: sandboxstore.Metadata{
				ID:     "2",
				Name:   "name-2",
				Config: &runtime.PodSandboxConfig{Metadata: &runtime.PodSandboxMetadata{Name: "name-2"}},
			},
		},
		{
			Metadata: sandboxstore.Metadata{
				ID:     "3",
				Name:   "name-3",
				Config: &runtime.PodSandboxConfig{Metadata: &runtime.PodSandboxMetadata{Name: "name-3"}},
			},
		},
	}
	sandboxesInContainerd := []task.Task{
		// Running container with corresponding metadata
		{
			ID:     "1",
			Pid:    1,
			Status: task.StatusRunning,
		},
		// Stopped container with corresponding metadata
		{
			ID:     "2",
			Pid:    2,
			Status: task.StatusStopped,
		},
		// Container without corresponding metadata
		{
			ID:     "4",
			Pid:    4,
			Status: task.StatusStopped,
		},
	}
	expect := []*runtime.PodSandbox{
		{
			Id:       "1",
			Metadata: &runtime.PodSandboxMetadata{Name: "name-1"},
			State:    runtime.PodSandboxState_SANDBOX_READY,
		},
		{
			Id:       "2",
			Metadata: &runtime.PodSandboxMetadata{Name: "name-2"},
			State:    runtime.PodSandboxState_SANDBOX_NOTREADY,
		},
		{
			Id:       "3",
			Metadata: &runtime.PodSandboxMetadata{Name: "name-3"},
			State:    runtime.PodSandboxState_SANDBOX_NOTREADY,
		},
	}

	// Inject test metadata
	for _, s := range sandboxesInStore {
		assert.NoError(t, c.sandboxStore.Add(s))
	}

	// Inject fake containerd tasks
	fake.SetFakeTasks(sandboxesInContainerd)

	resp, err := c.ListPodSandbox(context.Background(), &runtime.ListPodSandboxRequest{})
	assert.NoError(t, err)
	sandboxes := resp.GetItems()
	assert.Len(t, sandboxes, len(expect))
	for _, s := range expect {
		assert.Contains(t, sandboxes, s)
	}
}
