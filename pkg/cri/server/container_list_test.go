/*
   Copyright The containerd Authors.

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
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	containerstore "github.com/containerd/containerd/v2/pkg/cri/store/container"
	sandboxstore "github.com/containerd/containerd/v2/pkg/cri/store/sandbox"
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
	container, err := containerstore.NewContainer(
		containerstore.Metadata{
			ID:        "test-id",
			Name:      "test-name",
			SandboxID: "test-sandbox-id",
			Config:    config,
			ImageRef:  "test-image-ref",
		},
		containerstore.WithFakeStatus(
			containerstore.Status{
				Pid:        1234,
				CreatedAt:  createdAt,
				StartedAt:  time.Now().UnixNano(),
				FinishedAt: time.Now().UnixNano(),
				ExitCode:   1,
				Reason:     "test-reason",
				Message:    "test-message",
			},
		),
	)
	assert.NoError(t, err)
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
	c := toCRIContainer(container)
	assert.Equal(t, expect, c)
}

func TestFilterContainers(t *testing.T) {
	c := newTestCRIService()

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
	for _, test := range []struct {
		desc   string
		filter *runtime.ContainerFilter
		expect []*runtime.Container
	}{
		{
			desc:   "no filter",
			expect: testContainers,
		},
		{
			desc:   "id filter",
			filter: &runtime.ContainerFilter{Id: "2"},
			expect: []*runtime.Container{testContainers[1]},
		},
		{
			desc: "state filter",
			filter: &runtime.ContainerFilter{
				State: &runtime.ContainerStateValue{
					State: runtime.ContainerState_CONTAINER_EXITED,
				},
			},
			expect: []*runtime.Container{testContainers[1]},
		},
		{
			desc: "label filter",
			filter: &runtime.ContainerFilter{
				LabelSelector: map[string]string{"a": "b"},
			},
			expect: []*runtime.Container{testContainers[1]},
		},
		{
			desc:   "sandbox id filter",
			filter: &runtime.ContainerFilter{PodSandboxId: "s-2"},
			expect: []*runtime.Container{testContainers[1], testContainers[2]},
		},
		{
			desc: "mixed filter not matched",
			filter: &runtime.ContainerFilter{
				Id:            "1",
				PodSandboxId:  "s-2",
				LabelSelector: map[string]string{"a": "b"},
			},
			expect: []*runtime.Container{},
		},
		{
			desc: "mixed filter matched",
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
		test := test
		t.Run(test.desc, func(t *testing.T) {
			filtered := c.filterCRIContainers(testContainers, test.filter)
			assert.Equal(t, test.expect, filtered, test.desc)
		})
	}
}

// containerForTest is a helper type for test.
type containerForTest struct {
	metadata containerstore.Metadata
	status   containerstore.Status
}

func (c containerForTest) toContainer() (containerstore.Container, error) {
	return containerstore.NewContainer(
		c.metadata,
		containerstore.WithFakeStatus(c.status),
	)
}

func TestListContainers(t *testing.T) {
	c := newTestCRIService()
	sandboxesInStore := []sandboxstore.Sandbox{
		sandboxstore.NewSandbox(
			sandboxstore.Metadata{
				ID:     "s-1abcdef1234",
				Name:   "sandboxname-1",
				Config: &runtime.PodSandboxConfig{Metadata: &runtime.PodSandboxMetadata{Name: "podname-1"}},
			},
			sandboxstore.Status{
				State: sandboxstore.StateReady,
			},
		),
		sandboxstore.NewSandbox(
			sandboxstore.Metadata{
				ID:     "s-2abcdef1234",
				Name:   "sandboxname-2",
				Config: &runtime.PodSandboxConfig{Metadata: &runtime.PodSandboxMetadata{Name: "podname-2"}},
			},
			sandboxstore.Status{
				State: sandboxstore.StateNotReady,
			},
		),
	}
	createdAt := time.Now().UnixNano()
	startedAt := time.Now().UnixNano()
	finishedAt := time.Now().UnixNano()
	containersInStore := []containerForTest{
		{
			metadata: containerstore.Metadata{
				ID:        "c-1container",
				Name:      "name-1",
				SandboxID: "s-1abcdef1234",
				Config:    &runtime.ContainerConfig{Metadata: &runtime.ContainerMetadata{Name: "name-1"}},
			},
			status: containerstore.Status{CreatedAt: createdAt},
		},
		{
			metadata: containerstore.Metadata{
				ID:        "c-2container",
				Name:      "name-2",
				SandboxID: "s-1abcdef1234",
				Config:    &runtime.ContainerConfig{Metadata: &runtime.ContainerMetadata{Name: "name-2"}},
			},
			status: containerstore.Status{
				CreatedAt: createdAt,
				StartedAt: startedAt,
			},
		},
		{
			metadata: containerstore.Metadata{
				ID:        "c-3container",
				Name:      "name-3",
				SandboxID: "s-1abcdef1234",
				Config:    &runtime.ContainerConfig{Metadata: &runtime.ContainerMetadata{Name: "name-3"}},
			},
			status: containerstore.Status{
				CreatedAt:  createdAt,
				StartedAt:  startedAt,
				FinishedAt: finishedAt,
			},
		},
		{
			metadata: containerstore.Metadata{
				ID:        "c-4container",
				Name:      "name-4",
				SandboxID: "s-2abcdef1234",
				Config:    &runtime.ContainerConfig{Metadata: &runtime.ContainerMetadata{Name: "name-4"}},
			},
			status: containerstore.Status{
				CreatedAt: createdAt,
			},
		},
	}

	expectedContainers := []*runtime.Container{
		{
			Id:           "c-1container",
			PodSandboxId: "s-1abcdef1234",
			Metadata:     &runtime.ContainerMetadata{Name: "name-1"},
			State:        runtime.ContainerState_CONTAINER_CREATED,
			CreatedAt:    createdAt,
		},
		{
			Id:           "c-2container",
			PodSandboxId: "s-1abcdef1234",
			Metadata:     &runtime.ContainerMetadata{Name: "name-2"},
			State:        runtime.ContainerState_CONTAINER_RUNNING,
			CreatedAt:    createdAt,
		},
		{
			Id:           "c-3container",
			PodSandboxId: "s-1abcdef1234",
			Metadata:     &runtime.ContainerMetadata{Name: "name-3"},
			State:        runtime.ContainerState_CONTAINER_EXITED,
			CreatedAt:    createdAt,
		},
		{
			Id:           "c-4container",
			PodSandboxId: "s-2abcdef1234",
			Metadata:     &runtime.ContainerMetadata{Name: "name-4"},
			State:        runtime.ContainerState_CONTAINER_CREATED,
			CreatedAt:    createdAt,
		},
	}

	// Inject test sandbox metadata
	for _, sb := range sandboxesInStore {
		assert.NoError(t, c.sandboxStore.Add(sb))
	}

	// Inject test container metadata
	for _, cntr := range containersInStore {
		container, err := cntr.toContainer()
		assert.NoError(t, err)
		assert.NoError(t, c.containerStore.Add(container))
	}

	for _, testdata := range []struct {
		desc   string
		filter *runtime.ContainerFilter
		expect []*runtime.Container
	}{
		{
			desc:   "test without filter",
			filter: &runtime.ContainerFilter{},
			expect: expectedContainers,
		},
		{
			desc: "test filter by sandboxid",
			filter: &runtime.ContainerFilter{
				PodSandboxId: "s-1abcdef1234",
			},
			expect: expectedContainers[:3],
		},
		{
			desc: "test filter by truncated sandboxid",
			filter: &runtime.ContainerFilter{
				PodSandboxId: "s-1",
			},
			expect: expectedContainers[:3],
		},
		{
			desc: "test filter by containerid",
			filter: &runtime.ContainerFilter{
				Id: "c-1container",
			},
			expect: expectedContainers[:1],
		},
		{
			desc: "test filter by truncated containerid",
			filter: &runtime.ContainerFilter{
				Id: "c-1",
			},
			expect: expectedContainers[:1],
		},
		{
			desc: "test filter by containerid and sandboxid",
			filter: &runtime.ContainerFilter{
				Id:           "c-1container",
				PodSandboxId: "s-1abcdef1234",
			},
			expect: expectedContainers[:1],
		},
		{
			desc: "test filter by truncated containerid and truncated sandboxid",
			filter: &runtime.ContainerFilter{
				Id:           "c-1",
				PodSandboxId: "s-1",
			},
			expect: expectedContainers[:1],
		},
	} {
		testdata := testdata
		t.Run(testdata.desc, func(t *testing.T) {
			resp, err := c.ListContainers(context.Background(), &runtime.ListContainersRequest{Filter: testdata.filter})
			assert.NoError(t, err)
			require.NotNil(t, resp)
			containers := resp.GetContainers()
			assert.Len(t, containers, len(testdata.expect))
			for _, cntr := range testdata.expect {
				assert.Contains(t, containers, cntr)
			}
		})
	}
}
