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
	"errors"
	"testing"
	"time"

	"github.com/containerd/containerd/api/services/execution"
	"github.com/containerd/containerd/api/types/task"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"golang.org/x/net/context"
	"golang.org/x/sys/unix"
	"k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1/runtime"

	servertesting "github.com/kubernetes-incubator/cri-containerd/pkg/server/testing"
	containerstore "github.com/kubernetes-incubator/cri-containerd/pkg/store/container"
	imagestore "github.com/kubernetes-incubator/cri-containerd/pkg/store/image"
)

func TestWaitContainerStop(t *testing.T) {
	id := "test-id"
	for desc, test := range map[string]struct {
		status    *containerstore.Status
		cancel    bool
		timeout   time.Duration
		expectErr bool
	}{
		"should return error if timeout exceeds": {
			status: &containerstore.Status{
				CreatedAt: time.Now().UnixNano(),
				StartedAt: time.Now().UnixNano(),
			},
			timeout:   2 * stopCheckPollInterval,
			expectErr: true,
		},
		"should return error if context is cancelled": {
			status: &containerstore.Status{
				CreatedAt: time.Now().UnixNano(),
				StartedAt: time.Now().UnixNano(),
			},
			timeout:   time.Hour,
			cancel:    true,
			expectErr: true,
		},
		"should not return error if container is removed before timeout": {
			status:    nil,
			timeout:   time.Hour,
			expectErr: false,
		},
		"should not return error if container is stopped before timeout": {
			status: &containerstore.Status{
				CreatedAt:  time.Now().UnixNano(),
				StartedAt:  time.Now().UnixNano(),
				FinishedAt: time.Now().UnixNano(),
			},
			timeout:   time.Hour,
			expectErr: false,
		},
	} {
		c := newTestCRIContainerdService()
		if test.status != nil {
			container, err := containerstore.NewContainer(
				containerstore.Metadata{ID: id},
				*test.status,
			)
			assert.NoError(t, err)
			assert.NoError(t, c.containerStore.Add(container))
		}
		ctx := context.Background()
		if test.cancel {
			cancelledCtx, cancel := context.WithCancel(ctx)
			cancel()
			ctx = cancelledCtx
		}
		err := c.waitContainerStop(ctx, id, test.timeout)
		assert.Equal(t, test.expectErr, err != nil, desc)
	}
}

func TestStopContainer(t *testing.T) {
	testID := "test-id"
	testPid := uint32(1234)
	testImageID := "test-image-id"
	testStatus := containerstore.Status{
		Pid:       testPid,
		CreatedAt: time.Now().UnixNano(),
		StartedAt: time.Now().UnixNano(),
	}
	testImage := imagestore.Image{
		ID:     testImageID,
		Config: &imagespec.ImageConfig{},
	}
	testContainer := task.Task{
		ID:     testID,
		Pid:    testPid,
		Status: task.StatusRunning,
	}
	for desc, test := range map[string]struct {
		status              *containerstore.Status
		containerdContainer *task.Task
		stopSignal          string
		stopErr             error
		noTimeout           bool
		expectErr           bool
		expectCalls         []servertesting.CalledDetail
	}{
		"should return error when container does not exist": {
			status:      nil,
			expectErr:   true,
			expectCalls: []servertesting.CalledDetail{},
		},
		"should not return error when container is not running": {
			status:      &containerstore.Status{CreatedAt: time.Now().UnixNano()},
			expectErr:   false,
			expectCalls: []servertesting.CalledDetail{},
		},
		"should not return error if containerd task does not exist": {
			status:              &testStatus,
			containerdContainer: &testContainer,
			// Since it's hard to inject event during `StopContainer` is running,
			// we only test the case that first stop returns error, but container
			// status is not updated yet.
			// We also leverage this behavior to test that when graceful
			// stop doesn't take effect, container should be SIGKILL-ed.
			stopErr:   servertesting.TaskNotExistError,
			expectErr: false,
			expectCalls: []servertesting.CalledDetail{
				{
					Name: "kill",
					Argument: &execution.KillRequest{
						ContainerID: testID,
						Signal:      uint32(unix.SIGTERM),
						PidOrAll:    &execution.KillRequest_All{All: true},
					},
				},
				{
					Name: "kill",
					Argument: &execution.KillRequest{
						ContainerID: testID,
						Signal:      uint32(unix.SIGKILL),
						PidOrAll:    &execution.KillRequest_All{All: true},
					},
				},
				{
					Name:     "delete",
					Argument: &execution.DeleteRequest{ContainerID: testID},
				},
			},
		},
		"should not return error if containerd task process already finished": {
			status:              &testStatus,
			containerdContainer: &testContainer,
			stopErr:             errors.New("os: process already finished"),
			expectErr:           false,
			expectCalls: []servertesting.CalledDetail{
				{
					Name: "kill",
					Argument: &execution.KillRequest{
						ContainerID: testID,
						Signal:      uint32(unix.SIGTERM),
						PidOrAll:    &execution.KillRequest_All{All: true},
					},
				},
				{
					Name: "kill",
					Argument: &execution.KillRequest{
						ContainerID: testID,
						Signal:      uint32(unix.SIGKILL),
						PidOrAll:    &execution.KillRequest_All{All: true},
					},
				},
				{
					Name:     "delete",
					Argument: &execution.DeleteRequest{ContainerID: testID},
				},
			},
		},
		"should return error if graceful stop returns random error": {
			status:              &testStatus,
			containerdContainer: &testContainer,
			stopErr:             errors.New("random stop error"),
			expectErr:           true,
			expectCalls: []servertesting.CalledDetail{
				{
					Name: "kill",
					Argument: &execution.KillRequest{
						ContainerID: testID,
						Signal:      uint32(unix.SIGTERM),
						PidOrAll:    &execution.KillRequest_All{All: true},
					},
				},
			},
		},
		"should not return error if containerd task is gracefully stopped": {
			status:              &testStatus,
			containerdContainer: &testContainer,
			expectErr:           false,
			// deleted by the event monitor.
			expectCalls: []servertesting.CalledDetail{
				{
					Name: "kill",
					Argument: &execution.KillRequest{
						ContainerID: testID,
						Signal:      uint32(unix.SIGTERM),
						PidOrAll:    &execution.KillRequest_All{All: true},
					},
				},
				{
					Name:     "delete",
					Argument: &execution.DeleteRequest{ContainerID: testID},
				},
			},
		},
		"should use stop signal specified in image config if not empty": {
			status:              &testStatus,
			containerdContainer: &testContainer,
			stopSignal:          "SIGHUP",
			expectErr:           false,
			// deleted by the event monitor.
			expectCalls: []servertesting.CalledDetail{
				{
					Name: "kill",
					Argument: &execution.KillRequest{
						ContainerID: testID,
						Signal:      uint32(unix.SIGHUP),
						PidOrAll:    &execution.KillRequest_All{All: true},
					},
				},
				{
					Name:     "delete",
					Argument: &execution.DeleteRequest{ContainerID: testID},
				},
			},
		},
		"should directly kill container if timeout is 0": {
			status:              &testStatus,
			containerdContainer: &testContainer,
			noTimeout:           true,
			expectErr:           false,
			expectCalls: []servertesting.CalledDetail{
				{
					Name: "kill",
					Argument: &execution.KillRequest{
						ContainerID: testID,
						Signal:      uint32(unix.SIGKILL),
						PidOrAll:    &execution.KillRequest_All{All: true},
					},
				},
				{
					Name:     "delete",
					Argument: &execution.DeleteRequest{ContainerID: testID},
				},
			},
		},
		// TODO(random-liu): Test "should return error if both failed" after we have
		// fake clock for test.
	} {
		t.Logf("TestCase %q", desc)
		c := newTestCRIContainerdService()
		fake := servertesting.NewFakeExecutionClient().WithEvents()
		defer fake.Stop()
		c.taskService = fake

		// Inject the container.
		if test.status != nil {
			cntr, err := containerstore.NewContainer(
				containerstore.Metadata{
					ID:       testID,
					ImageRef: testImageID,
				},
				*test.status,
			)
			assert.NoError(t, err)
			assert.NoError(t, c.containerStore.Add(cntr))
		}
		// Inject containerd task.
		if test.containerdContainer != nil {
			fake.SetFakeTasks([]task.Task{*test.containerdContainer})
		}
		testImage.Config.StopSignal = test.stopSignal
		c.imageStore.Add(testImage)
		if test.stopErr != nil {
			fake.InjectError("kill", test.stopErr)
		}
		eventClient, err := fake.Events(context.Background(), &execution.EventsRequest{})
		assert.NoError(t, err)
		// Start a simple test event monitor.
		go func(e execution.Tasks_EventsClient) {
			for {
				if err := c.handleEventStream(e); err != nil { // nolint: vetshadow
					return
				}
			}
		}(eventClient)
		fake.ClearCalls()
		timeout := int64(1)
		if test.noTimeout {
			timeout = 0
		}
		// 1 second timeout should be enough for the unit test.
		// TODO(random-liu): Use fake clock for this test.
		resp, err := c.StopContainer(context.Background(), &runtime.StopContainerRequest{
			ContainerId: testID,
			Timeout:     timeout,
		})
		if test.expectErr {
			assert.Error(t, err)
			assert.Nil(t, resp)
		} else {
			assert.NoError(t, err)
			assert.NotNil(t, resp)
		}
		assert.Equal(t, test.expectCalls, fake.GetCalledDetails())
	}
}
