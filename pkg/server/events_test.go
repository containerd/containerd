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
	"github.com/containerd/containerd/api/types/task"
	"github.com/stretchr/testify/assert"
	"golang.org/x/net/context"
	"k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1/runtime"

	servertesting "github.com/kubernetes-incubator/cri-containerd/pkg/server/testing"
	containerstore "github.com/kubernetes-incubator/cri-containerd/pkg/store/container"
)

func TestHandleEvent(t *testing.T) {
	testID := "test-id"
	testPid := uint32(1234)
	testCreatedAt := time.Now().UnixNano()
	testStartedAt := time.Now().UnixNano()
	testMetadata := containerstore.Metadata{
		ID:        testID,
		Name:      "test-name",
		SandboxID: "test-sandbox-id",
	}
	// Container status in running state.
	testStatus := containerstore.Status{
		Pid:       testPid,
		CreatedAt: testCreatedAt,
		StartedAt: testStartedAt,
	}
	testExitedAt := time.Now()
	testExitEvent := task.Event{
		ID:         testID,
		Type:       task.Event_EXIT,
		Pid:        testPid,
		ExitStatus: 1,
		ExitedAt:   testExitedAt,
	}
	testFinishedStatus := containerstore.Status{
		Pid:        0,
		CreatedAt:  testCreatedAt,
		StartedAt:  testStartedAt,
		FinishedAt: testExitedAt.UnixNano(),
		ExitCode:   1,
	}
	assert.Equal(t, runtime.ContainerState_CONTAINER_RUNNING, testStatus.State())
	testContainerdContainer := task.Task{
		ID:     testID,
		Pid:    testPid,
		Status: task.StatusRunning,
	}

	for desc, test := range map[string]struct {
		event               *task.Event
		status              *containerstore.Status
		containerdContainer *task.Task
		containerdErr       error
		expected            *containerstore.Status
	}{
		"should not update state when no corresponding container for event": {
			event:    &testExitEvent,
			expected: nil,
		},
		"should not update state when exited process is not init process": {
			event: &task.Event{
				ID:         testID,
				Type:       task.Event_EXIT,
				Pid:        9999,
				ExitStatus: 1,
				ExitedAt:   testExitedAt,
			},
			status:              &testStatus,
			containerdContainer: &testContainerdContainer,
			expected:            &testStatus,
		},
		"should not update state when fail to delete containerd task": {
			event:               &testExitEvent,
			status:              &testStatus,
			containerdContainer: &testContainerdContainer,
			containerdErr:       fmt.Errorf("random error"),
			expected:            &testStatus,
		},
		"should not update state for irrelevant events": {
			event: &task.Event{
				ID:   testID,
				Type: task.Event_PAUSED,
				Pid:  testPid,
			},
			status:              &testStatus,
			containerdContainer: &testContainerdContainer,
			expected:            &testStatus,
		},
		"should update state when containerd task is already deleted": {
			event:    &testExitEvent,
			status:   &testStatus,
			expected: &testFinishedStatus,
		},
		"should update state when delete containerd task successfully": {
			event:               &testExitEvent,
			status:              &testStatus,
			containerdContainer: &testContainerdContainer,
			expected:            &testFinishedStatus,
		},
		"should update exit reason when container is oom killed": {
			event: &task.Event{
				ID:   testID,
				Type: task.Event_OOM,
			},
			status: &testStatus,
			expected: &containerstore.Status{
				Pid:       testPid,
				CreatedAt: testCreatedAt,
				StartedAt: testStartedAt,
				Reason:    oomExitReason,
			},
		},
	} {
		t.Logf("TestCase %q", desc)
		c := newTestCRIContainerdService()
		fake := c.taskService.(*servertesting.FakeExecutionClient)
		e, err := fake.Events(context.Background(), &execution.EventsRequest{})
		assert.NoError(t, err)
		fakeEvents := e.(*servertesting.EventClient)
		// Inject event.
		if test.event != nil {
			fakeEvents.Events <- test.event
		}
		// Inject internal container object.
		if test.status != nil {
			cntr, err := containerstore.NewContainer( // nolint: vetshadow
				testMetadata,
				*test.status,
			)
			assert.NoError(t, err)
			assert.NoError(t, c.containerStore.Add(cntr))
		}
		// Inject containerd task.
		if test.containerdContainer != nil {
			fake.SetFakeTasks([]task.Task{*test.containerdContainer})
		}
		// Inject containerd delete error.
		if test.containerdErr != nil {
			fake.InjectError("delete", test.containerdErr)
		}
		assert.NoError(t, c.handleEventStream(e))
		if test.expected == nil {
			continue
		}
		got, err := c.containerStore.Get(testID)
		assert.NoError(t, err)
		assert.Equal(t, *test.expected, got.Status.Get())
	}
}
