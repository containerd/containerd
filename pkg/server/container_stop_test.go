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
	"github.com/containerd/containerd/api/types/container"
	"github.com/stretchr/testify/assert"
	"golang.org/x/net/context"
	runtime "k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1"

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
		CreatedAt: time.Now().UnixNano(),
		StartedAt: time.Now().UnixNano(),
	}
	testContainer := container.Container{
		ID:     testID,
		Pid:    testPid,
		Status: container.Status_RUNNING,
	}
	for desc, test := range map[string]struct {
		metadata            *metadata.ContainerMetadata
		containerdContainer *container.Container
		killErr             error
		deleteErr           error
		discardEvents       int
		noTimeout           bool
		expectErr           bool
		expectCalls         []string
	}{
		"should return error when container does not exist": {
			metadata:    nil,
			expectErr:   true,
			expectCalls: []string{},
		},
		"should not return error when container is not running": {
			metadata: &metadata.ContainerMetadata{
				ID:        testID,
				CreatedAt: time.Now().UnixNano(),
			},
			expectErr:   false,
			expectCalls: []string{},
		},
		"should not return error if containerd container does not exist": {
			metadata:    &testMetadata,
			expectErr:   false,
			expectCalls: []string{"kill"},
		},
		"should not return error if containerd container is killed": {
			metadata:            &testMetadata,
			containerdContainer: &testContainer,
			expectErr:           false,
			// deleted by the event monitor.
			expectCalls: []string{"kill", "delete"},
		},
		"should not return error if containerd container is deleted": {
			metadata:            &testMetadata,
			containerdContainer: &testContainer,
			// discard killed events to force a delete. This is only
			// for testing. Actually real containerd should only generate
			// one EXIT event.
			discardEvents: 1,
			expectErr:     false,
			// one more delete from the event monitor.
			expectCalls: []string{"kill", "delete", "delete"},
		},
		"should return error if kill failed": {
			metadata:            &testMetadata,
			containerdContainer: &testContainer,
			killErr:             errors.New("random error"),
			expectErr:           true,
			expectCalls:         []string{"kill"},
		},
		"should directly kill container if timeout is 0": {
			metadata:            &testMetadata,
			containerdContainer: &testContainer,
			noTimeout:           true,
			expectCalls:         []string{"delete", "delete"},
		},
		"should return error if delete failed": {
			metadata:            &testMetadata,
			containerdContainer: &testContainer,
			deleteErr:           errors.New("random error"),
			discardEvents:       1,
			expectErr:           true,
			expectCalls:         []string{"kill", "delete"},
		},
	} {
		t.Logf("TestCase %q", desc)
		c := newTestCRIContainerdService()
		fake := servertesting.NewFakeExecutionClient().WithEvents()
		defer fake.Stop()
		c.containerService = fake

		// Inject metadata.
		if test.metadata != nil {
			assert.NoError(t, c.containerStore.Create(*test.metadata))
		}
		// Inject containerd container.
		if test.containerdContainer != nil {
			fake.SetFakeContainers([]container.Container{*test.containerdContainer})
		}
		if test.killErr != nil {
			fake.InjectError("kill", test.killErr)
		}
		if test.deleteErr != nil {
			fake.InjectError("delete", test.deleteErr)
		}
		eventClient, err := fake.Events(context.Background(), &execution.EventsRequest{})
		assert.NoError(t, err)
		// Start a simple test event monitor.
		go func(e execution.ContainerService_EventsClient, discard int) {
			for {
				e, err := e.Recv() // nolint: vetshadow
				if err != nil {
					return
				}
				if discard > 0 {
					discard--
					continue
				}
				c.handleEvent(e)
			}
		}(eventClient, test.discardEvents)
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
		assert.Equal(t, test.expectCalls, fake.GetCalledNames())
	}
}
