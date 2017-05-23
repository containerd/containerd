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
	"testing"
	"time"

	"github.com/containerd/containerd/api/services/execution"
	"github.com/containerd/containerd/api/types/container"
	"github.com/stretchr/testify/assert"
	"golang.org/x/net/context"
	runtime "k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1"

	"github.com/kubernetes-incubator/cri-containerd/pkg/metadata"
	servertesting "github.com/kubernetes-incubator/cri-containerd/pkg/server/testing"
)

func TestHandleEvent(t *testing.T) {
	testID := "test-id"
	testPid := uint32(1234)
	testCreatedAt := time.Now().UnixNano()
	testStartedAt := time.Now().UnixNano()
	// Container metadata in running state.
	testMetadata := metadata.ContainerMetadata{
		ID:        testID,
		Name:      "test-name",
		SandboxID: "test-sandbox-id",
		Pid:       testPid,
		CreatedAt: testCreatedAt,
		StartedAt: testStartedAt,
	}
	testExitedAt := time.Now()
	testExitEvent := container.Event{
		ID:         testID,
		Type:       container.Event_EXIT,
		Pid:        testPid,
		ExitStatus: 1,
		ExitedAt:   testExitedAt,
	}
	testFinishedMetadata := metadata.ContainerMetadata{
		ID:         testID,
		Name:       "test-name",
		SandboxID:  "test-sandbox-id",
		Pid:        0,
		CreatedAt:  testCreatedAt,
		StartedAt:  testStartedAt,
		FinishedAt: testExitedAt.UnixNano(),
		ExitCode:   1,
	}
	assert.Equal(t, runtime.ContainerState_CONTAINER_RUNNING, testMetadata.State())
	testContainerdContainer := container.Container{
		ID:     testID,
		Pid:    testPid,
		Status: container.Status_RUNNING,
	}

	for desc, test := range map[string]struct {
		event               *container.Event
		metadata            *metadata.ContainerMetadata
		containerdContainer *container.Container
		containerdErr       error
		expected            *metadata.ContainerMetadata
	}{
		"should not update state when no corresponding metadata for event": {
			event:    &testExitEvent,
			expected: nil,
		},
		"should not update state when exited process is not init process": {
			event: &container.Event{
				ID:         testID,
				Type:       container.Event_EXIT,
				Pid:        9999,
				ExitStatus: 1,
				ExitedAt:   testExitedAt,
			},
			metadata:            &testMetadata,
			containerdContainer: &testContainerdContainer,
			expected:            &testMetadata,
		},
		"should not update state when fail to delete containerd container": {
			event:               &testExitEvent,
			metadata:            &testMetadata,
			containerdContainer: &testContainerdContainer,
			containerdErr:       fmt.Errorf("random error"),
			expected:            &testMetadata,
		},
		"should not update state for non-exited events": {
			event: &container.Event{
				ID:         testID,
				Type:       container.Event_OOM,
				Pid:        testPid,
				ExitStatus: 1,
				ExitedAt:   testExitedAt,
			},
			metadata:            &testMetadata,
			containerdContainer: &testContainerdContainer,
			expected:            &testMetadata,
		},
		"should update state when containerd container is already deleted": {
			event:    &testExitEvent,
			metadata: &testMetadata,
			expected: &testFinishedMetadata,
		},
		"should update state when delete containerd container successfully": {
			event:               &testExitEvent,
			metadata:            &testMetadata,
			containerdContainer: &testContainerdContainer,
			expected:            &testFinishedMetadata,
		},
	} {
		t.Logf("TestCase %q", desc)
		c := newTestCRIContainerdService()
		fake := c.containerService.(*servertesting.FakeExecutionClient)
		e, err := fake.Events(context.Background(), &execution.EventsRequest{})
		assert.NoError(t, err)
		fakeEvents := e.(*servertesting.EventClient)
		// Inject event.
		if test.event != nil {
			fakeEvents.Events <- test.event
		}
		// Inject metadata.
		if test.metadata != nil {
			// Make sure that original data will not be changed.
			assert.NoError(t, c.containerStore.Create(*test.metadata))
		}
		// Inject containerd container.
		if test.containerdContainer != nil {
			fake.SetFakeContainers([]container.Container{*test.containerdContainer})
		}
		// Inject containerd delete error.
		if test.containerdErr != nil {
			fake.InjectError("delete", test.containerdErr)
		}
		c.handleEventStream(e)
		got, _ := c.containerStore.Get(testID)
		assert.Equal(t, test.expected, got)
	}
}
