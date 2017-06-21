/*
Copyright 2017 The Kubernetes Authorc.

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

package metadata

import (
	"testing"
	"time"

	assertlib "github.com/stretchr/testify/assert"

	"github.com/kubernetes-incubator/cri-containerd/pkg/metadata/store"

	"k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1/runtime"
)

func TestContainerState(t *testing.T) {
	for c, test := range map[string]struct {
		metadata *ContainerMetadata
		state    runtime.ContainerState
	}{
		"unknown state": {
			metadata: &ContainerMetadata{
				ID:   "1",
				Name: "Container-1",
			},
			state: runtime.ContainerState_CONTAINER_UNKNOWN,
		},
		"created state": {
			metadata: &ContainerMetadata{
				ID:        "2",
				Name:      "Container-2",
				CreatedAt: time.Now().UnixNano(),
			},
			state: runtime.ContainerState_CONTAINER_CREATED,
		},
		"running state": {
			metadata: &ContainerMetadata{
				ID:        "3",
				Name:      "Container-3",
				CreatedAt: time.Now().UnixNano(),
				StartedAt: time.Now().UnixNano(),
			},
			state: runtime.ContainerState_CONTAINER_RUNNING,
		},
		"exited state": {
			metadata: &ContainerMetadata{
				ID:         "3",
				Name:       "Container-3",
				CreatedAt:  time.Now().UnixNano(),
				FinishedAt: time.Now().UnixNano(),
			},
			state: runtime.ContainerState_CONTAINER_EXITED,
		},
	} {
		t.Logf("TestCase %q", c)
		assertlib.Equal(t, test.state, test.metadata.State())
	}
}

func TestContainerStore(t *testing.T) {
	containers := map[string]*ContainerMetadata{
		"1": {
			ID:        "1",
			Name:      "Container-1",
			SandboxID: "Sandbox-1",
			Config: &runtime.ContainerConfig{
				Metadata: &runtime.ContainerMetadata{
					Name:    "TestPod-1",
					Attempt: 1,
				},
			},
			ImageRef:   "TestImage-1",
			Pid:        1,
			CreatedAt:  time.Now().UnixNano(),
			StartedAt:  time.Now().UnixNano(),
			FinishedAt: time.Now().UnixNano(),
			ExitCode:   1,
			Reason:     "TestReason-1",
			Message:    "TestMessage-1",
		},
		"2": {
			ID:        "2",
			Name:      "Container-2",
			SandboxID: "Sandbox-2",
			Config: &runtime.ContainerConfig{
				Metadata: &runtime.ContainerMetadata{
					Name:    "TestPod-2",
					Attempt: 2,
				},
			},
			ImageRef:   "TestImage-2",
			Pid:        2,
			CreatedAt:  time.Now().UnixNano(),
			StartedAt:  time.Now().UnixNano(),
			FinishedAt: time.Now().UnixNano(),
			ExitCode:   2,
			Reason:     "TestReason-2",
			Message:    "TestMessage-2",
		},
		"3": {
			ID:        "3",
			Name:      "Container-3",
			SandboxID: "Sandbox-3",
			Config: &runtime.ContainerConfig{
				Metadata: &runtime.ContainerMetadata{
					Name:    "TestPod-3",
					Attempt: 3,
				},
			},
			ImageRef:   "TestImage-3",
			Pid:        3,
			CreatedAt:  time.Now().UnixNano(),
			StartedAt:  time.Now().UnixNano(),
			FinishedAt: time.Now().UnixNano(),
			ExitCode:   3,
			Reason:     "TestReason-3",
			Message:    "TestMessage-3",
			Removing:   true,
		},
	}
	assert := assertlib.New(t)

	c := NewContainerStore(store.NewMetadataStore())

	t.Logf("should be able to create container metadata")
	for _, meta := range containers {
		assert.NoError(c.Create(*meta))
	}

	t.Logf("should be able to get container metadata")
	for id, expectMeta := range containers {
		meta, err := c.Get(id)
		assert.NoError(err)
		assert.Equal(expectMeta, meta)
	}

	t.Logf("should be able to list container metadata")
	cntrs, err := c.List()
	assert.NoError(err)
	assert.Len(cntrs, 3)

	t.Logf("should be able to update container metadata")
	testID := "2"
	newCreatedAt := time.Now().UnixNano()
	expectMeta := *containers[testID]
	expectMeta.CreatedAt = newCreatedAt
	err = c.Update(testID, func(o ContainerMetadata) (ContainerMetadata, error) {
		o.CreatedAt = newCreatedAt
		return o, nil
	})
	assert.NoError(err)
	newMeta, err := c.Get(testID)
	assert.NoError(err)
	assert.Equal(&expectMeta, newMeta)

	t.Logf("should be able to delete container metadata")
	assert.NoError(c.Delete(testID))
	cntrs, err = c.List()
	assert.NoError(err)
	assert.Len(cntrs, 2)

	t.Logf("get should return nil without error after deletion")
	meta, err := c.Get(testID)
	assert.Error(store.ErrNotExist, err)
	assert.True(meta == nil)
}
