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
	"os"
	"testing"
	"time"

	"github.com/containerd/containerd/api/services/execution"
	"github.com/containerd/containerd/api/types/task"
	"github.com/stretchr/testify/assert"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	runtime "k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1"

	"github.com/kubernetes-incubator/cri-containerd/pkg/metadata"
	ostesting "github.com/kubernetes-incubator/cri-containerd/pkg/os/testing"
	servertesting "github.com/kubernetes-incubator/cri-containerd/pkg/server/testing"
)

func TestStopPodSandbox(t *testing.T) {
	testID := "test-id"
	testSandbox := metadata.SandboxMetadata{
		ID:   testID,
		Name: "test-name",
		Config: &runtime.PodSandboxConfig{
			Metadata: &runtime.PodSandboxMetadata{
				Name:      "test-name",
				Uid:       "test-uid",
				Namespace: "test-ns",
			}},
		NetNS: "test-netns",
	}
	testContainer := task.Task{
		ID:     testID,
		Pid:    1,
		Status: task.StatusRunning,
	}

	for desc, test := range map[string]struct {
		sandboxTasks     []task.Task
		injectSandbox    bool
		injectErr        error
		injectStatErr    error
		injectCNIErr     error
		injectUnmountErr error
		expectErr        bool
		expectCalls      []string
		expectedCNICalls []string
	}{
		"stop non-existing sandbox": {
			injectSandbox:    false,
			expectErr:        true,
			expectCalls:      []string{},
			expectedCNICalls: []string{},
		},
		"stop sandbox with sandbox container": {
			sandboxTasks:     []task.Task{testContainer},
			injectSandbox:    true,
			expectErr:        false,
			expectCalls:      []string{"delete"},
			expectedCNICalls: []string{"TearDownPod"},
		},
		"stop sandbox with sandbox container not exist error": {
			sandboxTasks:  []task.Task{},
			injectSandbox: true,
			// Inject error to make sure fake execution client returns error.
			injectErr:        servertesting.TaskNotExistError,
			expectErr:        false,
			expectCalls:      []string{"delete"},
			expectedCNICalls: []string{"TearDownPod"},
		},
		"stop sandbox with with arbitrary error": {
			injectSandbox:    true,
			injectErr:        grpc.Errorf(codes.Unknown, "arbitrary error"),
			expectErr:        true,
			expectCalls:      []string{"delete"},
			expectedCNICalls: []string{"TearDownPod"},
		},
		"stop sandbox with Stat returns arbitrary error": {
			sandboxTasks:     []task.Task{testContainer},
			injectSandbox:    true,
			expectErr:        true,
			injectStatErr:    errors.New("arbitrary error"),
			expectCalls:      []string{},
			expectedCNICalls: []string{},
		},
		"stop sandbox with Stat returns not exist error": {
			sandboxTasks:     []task.Task{testContainer},
			injectSandbox:    true,
			expectErr:        false,
			expectCalls:      []string{"delete"},
			injectStatErr:    os.ErrNotExist,
			expectedCNICalls: []string{},
		},
		"stop sandbox with TearDownPod fails": {
			sandboxTasks:     []task.Task{testContainer},
			injectSandbox:    true,
			expectErr:        true,
			expectedCNICalls: []string{"TearDownPod"},
			injectCNIErr:     errors.New("arbitrary error"),
			expectCalls:      []string{},
		},
		"stop sandbox with unmount error": {
			sandboxTasks:     []task.Task{testContainer},
			injectSandbox:    true,
			expectErr:        true,
			expectedCNICalls: []string{"TearDownPod"},
			injectCNIErr:     errors.New("arbitrary error"),
			injectUnmountErr: errors.New("arbitrary error"),
			expectCalls:      []string{},
		},
	} {
		t.Logf("TestCase %q", desc)
		c := newTestCRIContainerdService()
		fake := c.taskService.(*servertesting.FakeExecutionClient)
		fakeCNIPlugin := c.netPlugin.(*servertesting.FakeCNIPlugin)
		fakeOS := c.os.(*ostesting.FakeOS)
		fake.SetFakeTasks(test.sandboxTasks)

		if test.injectSandbox {
			assert.NoError(t, c.sandboxStore.Create(testSandbox))
			c.sandboxIDIndex.Add(testID)
		}
		if test.injectErr != nil {
			fake.InjectError("delete", test.injectErr)
		}
		if test.injectCNIErr != nil {
			fakeCNIPlugin.InjectError("TearDownPod", test.injectCNIErr)
		}
		if test.injectStatErr != nil {
			fakeOS.InjectError("Stat", test.injectStatErr)
		}
		if test.injectUnmountErr != nil {
			fakeOS.InjectError("Unmount", test.injectUnmountErr)
		}
		fakeCNIPlugin.SetFakePodNetwork(testSandbox.NetNS, testSandbox.Config.GetMetadata().GetNamespace(),
			testSandbox.Config.GetMetadata().GetName(), testID, sandboxStatusTestIP)

		res, err := c.StopPodSandbox(context.Background(), &runtime.StopPodSandboxRequest{
			PodSandboxId: testID,
		})
		if test.expectErr {
			assert.Error(t, err)
			assert.Nil(t, res)
		} else {
			assert.NoError(t, err)
			assert.NotNil(t, res)
		}
		assert.Equal(t, test.expectCalls, fake.GetCalledNames())
		assert.Equal(t, test.expectedCNICalls, fakeCNIPlugin.GetCalledNames())
	}
}

func TestStopContainersInSandbox(t *testing.T) {
	testID := "test-id"
	testSandbox := metadata.SandboxMetadata{
		ID:   testID,
		Name: "test-name",
		Config: &runtime.PodSandboxConfig{
			Metadata: &runtime.PodSandboxMetadata{
				Name:      "test-name",
				Uid:       "test-uid",
				Namespace: "test-ns",
			}},
		NetNS: "test-netns",
	}
	testContainers := []metadata.ContainerMetadata{
		{
			ID:        "test-cid-1",
			Name:      "test-cname-1",
			SandboxID: testID,
			Pid:       2,
			StartedAt: time.Now().UnixNano(),
		},
		{
			ID:        "test-cid-2",
			Name:      "test-cname-2",
			SandboxID: testID,
			Pid:       3,
			StartedAt: time.Now().UnixNano(),
		},
		{
			ID:        "test-cid-3",
			Name:      "test-cname-3",
			SandboxID: "other-sandbox-id",
			Pid:       4,
			StartedAt: time.Now().UnixNano(),
		},
	}
	testContainerdContainers := []task.Task{
		{
			ID:     testID,
			Pid:    1,
			Status: task.StatusRunning,
		},
		{
			ID:     "test-cid-1",
			Pid:    2,
			Status: task.StatusRunning,
		},
		{
			ID:     "test-cid-2",
			Pid:    3,
			Status: task.StatusRunning,
		},
		{
			ID:     "test-cid-3",
			Pid:    4,
			Status: task.StatusRunning,
		},
	}

	c := newTestCRIContainerdService()
	fake := servertesting.NewFakeExecutionClient().WithEvents()
	defer fake.Stop()
	c.taskService = fake
	fake.SetFakeTasks(testContainerdContainers)
	assert.NoError(t, c.sandboxStore.Create(testSandbox))
	assert.NoError(t, c.sandboxIDIndex.Add(testID))
	for _, cntr := range testContainers {
		assert.NoError(t, c.containerStore.Create(cntr))
	}

	fakeCNIPlugin := c.netPlugin.(*servertesting.FakeCNIPlugin)
	fakeCNIPlugin.SetFakePodNetwork(testSandbox.NetNS, testSandbox.Config.GetMetadata().GetNamespace(),
		testSandbox.Config.GetMetadata().GetName(), testID, sandboxStatusTestIP)

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
	res, err := c.StopPodSandbox(context.Background(), &runtime.StopPodSandboxRequest{
		PodSandboxId: testID,
	})
	assert.NoError(t, err)
	assert.NotNil(t, res)

	cntrs, err := c.containerStore.List()
	assert.NoError(t, err)
	assert.Len(t, cntrs, 3)
	expectedStates := map[string]runtime.ContainerState{
		"test-cid-1": runtime.ContainerState_CONTAINER_EXITED,
		"test-cid-2": runtime.ContainerState_CONTAINER_EXITED,
		"test-cid-3": runtime.ContainerState_CONTAINER_RUNNING,
	}
	for id, expected := range expectedStates {
		cntr, err := c.containerStore.Get(id)
		assert.NoError(t, err)
		assert.Equal(t, expected, cntr.State())
	}
}
