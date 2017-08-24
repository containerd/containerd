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

package container

import (
	"testing"
	"time"

	assertlib "github.com/stretchr/testify/assert"
	"k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1/runtime"

	cio "github.com/kubernetes-incubator/cri-containerd/pkg/server/io"
	"github.com/kubernetes-incubator/cri-containerd/pkg/store"
)

func TestContainerStore(t *testing.T) {
	ids := []string{"1", "2", "3"}
	metadatas := map[string]Metadata{
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
			ImageRef: "TestImage-1",
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
			ImageRef: "TestImage-2",
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
			ImageRef: "TestImage-3",
		},
	}
	statuses := map[string]Status{
		"1": {
			Pid:        1,
			CreatedAt:  time.Now().UnixNano(),
			StartedAt:  time.Now().UnixNano(),
			FinishedAt: time.Now().UnixNano(),
			ExitCode:   1,
			Reason:     "TestReason-1",
			Message:    "TestMessage-1",
		},
		"2": {
			Pid:        2,
			CreatedAt:  time.Now().UnixNano(),
			StartedAt:  time.Now().UnixNano(),
			FinishedAt: time.Now().UnixNano(),
			ExitCode:   2,
			Reason:     "TestReason-2",
			Message:    "TestMessage-2",
		},
		"3": {
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
	containers := map[string]Container{}
	for _, id := range ids {
		container, err := NewContainer(metadatas[id], statuses[id])
		assert.NoError(err)
		containers[id] = container
	}

	s := NewStore()

	t.Logf("should be able to add container")
	for _, c := range containers {
		assert.NoError(s.Add(c))
	}

	t.Logf("should be able to get container")
	for id, c := range containers {
		got, err := s.Get(id)
		assert.NoError(err)
		assert.Equal(c, got)
	}

	t.Logf("should be able to list containers")
	cs := s.List()
	assert.Len(cs, 3)

	testID := "2"
	t.Logf("add should return already exists error for duplicated container")
	assert.Equal(store.ErrAlreadyExist, s.Add(containers[testID]))

	t.Logf("should be able to delete container")
	s.Delete(testID)
	cs = s.List()
	assert.Len(cs, 2)

	t.Logf("get should return not exist error after deletion")
	c, err := s.Get(testID)
	assert.Equal(Container{}, c)
	assert.Equal(store.ErrNotExist, err)
}

func TestWithContainerIO(t *testing.T) {
	meta := Metadata{
		ID:        "1",
		Name:      "Container-1",
		SandboxID: "Sandbox-1",
		Config: &runtime.ContainerConfig{
			Metadata: &runtime.ContainerMetadata{
				Name:    "TestPod-1",
				Attempt: 1,
			},
		},
		ImageRef: "TestImage-1",
	}
	status := Status{
		Pid:        1,
		CreatedAt:  time.Now().UnixNano(),
		StartedAt:  time.Now().UnixNano(),
		FinishedAt: time.Now().UnixNano(),
		ExitCode:   1,
		Reason:     "TestReason-1",
		Message:    "TestMessage-1",
	}
	assert := assertlib.New(t)

	c, err := NewContainer(meta, status)
	assert.NoError(err)
	assert.Nil(c.IO)

	c, err = NewContainer(meta, status, WithContainerIO(&cio.ContainerIO{}))
	assert.NoError(err)
	assert.NotNil(c.IO)
}
