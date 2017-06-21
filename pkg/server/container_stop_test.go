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

	"github.com/kubernetes-incubator/cri-containerd/pkg/metadata"
	servertesting "github.com/kubernetes-incubator/cri-containerd/pkg/server/testing"
)

func TestWaitContainerStop(t *testing.T) {
	id := "test-id"
	for desc, test := range map[string]struct {
		metadata  *metadata.ContainerMetadata
		cancel    bool
		timeout   time.Duration
		expectErr bool
	}{
		"should return error if timeout exceeds": {
			metadata: &metadata.ContainerMetadata{
				ID:        id,
				CreatedAt: time.Now().UnixNano(),
				StartedAt: time.Now().UnixNano(),
			},
			timeout:   2 * stopCheckPollInterval,
			expectErr: true,
		},
		"should return error if context is cancelled": {
			metadata: &metadata.ContainerMetadata{
				ID:        id,
				CreatedAt: time.Now().UnixNano(),
				StartedAt: time.Now().UnixNano(),
			},
			timeout:   time.Hour,
			cancel:    true,
			expectErr: true,
		},
		"should not return error if container is removed before timeout": {
			metadata:  nil,
			timeout:   time.Hour,
			expectErr: false,
		},
		"should not return error if container is stopped before timeout": {
			metadata: &metadata.ContainerMetadata{
				ID:         id,
				CreatedAt:  time.Now().UnixNano(),
				StartedAt:  time.Now().UnixNano(),
				FinishedAt: time.Now().UnixNano(),
			},
			timeout:   time.Hour,
			expectErr: false,
		},
	} {
		c := newTestCRIContainerdService()
		if test.metadata != nil {
			assert.NoError(t, c.containerStore.Create(*test.metadata))
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
	testMetadata := metadata.ContainerMetadata{
		ID:        testID,
		Pid:       testPid,
		ImageRef:  "test-image-id",
		CreatedAt: time.Now().UnixNano(),
		StartedAt: time.Now().UnixNano(),
	}
	testImageMetadata := metadata.ImageMetadata{
		ID:     "test-image-id",
		Config: &imagespec.ImageConfig{},
	}
	testContainer := task.Task{
		ID:     testID,
		Pid:    testPid,
		Status: task.StatusRunning,
	}
	for desc, test := range map[string]struct {
		metadata            *metadata.ContainerMetadata
		containerdContainer *task.Task
		stopSignal          string
		stopErr             error
		noTimeout           bool
		expectErr           bool
		expectCalls         []servertesting.CalledDetail
	}{
		"should return error when container does not exist": {
			metadata:    nil,
			expectErr:   true,
			expectCalls: []servertesting.CalledDetail{},
		},
		"should not return error when container is not running": {
			metadata: &metadata.ContainerMetadata{
				ID:        testID,
				CreatedAt: time.Now().UnixNano(),
			},
			expectErr:   false,
			expectCalls: []servertesting.CalledDetail{},
		},
		"should not return error if containerd task does not exist": {
			metadata:            &testMetadata,
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
			metadata:            &testMetadata,
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
			metadata:            &testMetadata,
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
			metadata:            &testMetadata,
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
			metadata:            &testMetadata,
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
			metadata:            &testMetadata,
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

		// Inject metadata.
		if test.metadata != nil {
			assert.NoError(t, c.containerStore.Create(*test.metadata))
		}
		// Inject containerd task.
		if test.containerdContainer != nil {
			fake.SetFakeTasks([]task.Task{*test.containerdContainer})
		}
		testImageMetadata.Config.StopSignal = test.stopSignal
		assert.NoError(t, c.imageMetadataStore.Create(testImageMetadata))
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
