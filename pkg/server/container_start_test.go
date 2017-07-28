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
	"io"
	"os"
	"testing"
	"time"

	"github.com/containerd/containerd/api/services/execution"
	"github.com/containerd/containerd/api/types/mount"
	"github.com/containerd/containerd/api/types/task"
	"github.com/stretchr/testify/assert"
	"golang.org/x/net/context"
	"k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1/runtime"

	ostesting "github.com/kubernetes-incubator/cri-containerd/pkg/os/testing"
	servertesting "github.com/kubernetes-incubator/cri-containerd/pkg/server/testing"
	containerstore "github.com/kubernetes-incubator/cri-containerd/pkg/store/container"
	sandboxstore "github.com/kubernetes-incubator/cri-containerd/pkg/store/sandbox"
)

func TestStartContainer(t *testing.T) {
	testID := "test-id"
	testSandboxID := "test-sandbox-id"
	testMetadata := containerstore.Metadata{
		ID:        testID,
		Name:      "test-name",
		SandboxID: testSandboxID,
	}
	testStatus := &containerstore.Status{CreatedAt: time.Now().UnixNano()}
	testSandbox := &sandboxstore.Sandbox{
		Metadata: sandboxstore.Metadata{
			ID:   testSandboxID,
			Name: "test-sandbox-name",
		},
	}
	testSandboxContainer := &task.Task{
		ID:     testSandboxID,
		Pid:    uint32(4321),
		Status: task.StatusRunning,
	}
	testMounts := []*mount.Mount{{Type: "bind", Source: "test-source"}}
	for desc, test := range map[string]struct {
		status                     *containerstore.Status
		sandbox                    *sandboxstore.Sandbox
		sandboxContainerdContainer *task.Task
		snapshotMountsErr          bool
		prepareFIFOErr             error
		createContainerErr         error
		startContainerErr          error
		expectStateChange          bool
		expectCalls                []string
		expectErr                  bool
	}{
		"should return error when container does not exist": {
			status:                     nil,
			sandbox:                    testSandbox,
			sandboxContainerdContainer: testSandboxContainer,
			expectCalls:                []string{},
			expectErr:                  true,
		},
		"should return error when container is not in created state": {
			status: &containerstore.Status{
				CreatedAt: time.Now().UnixNano(),
				StartedAt: time.Now().UnixNano(),
			},
			sandbox:                    testSandbox,
			sandboxContainerdContainer: testSandboxContainer,
			expectCalls:                []string{},
			expectErr:                  true,
		},
		"should return error when container is in removing state": {
			status: &containerstore.Status{
				CreatedAt: time.Now().UnixNano(),
				Removing:  true,
			},
			sandbox:                    testSandbox,
			sandboxContainerdContainer: testSandboxContainer,
			expectCalls:                []string{},
			expectErr:                  true,
		},
		"should return error when sandbox does not exist": {
			status:                     testStatus,
			sandbox:                    nil,
			sandboxContainerdContainer: testSandboxContainer,
			expectStateChange:          true,
			expectCalls:                []string{},
			expectErr:                  true,
		},
		"should return error when sandbox is not running": {
			status:  testStatus,
			sandbox: testSandbox,
			sandboxContainerdContainer: &task.Task{
				ID:     testSandboxID,
				Pid:    uint32(4321),
				Status: task.StatusStopped,
			},
			expectStateChange: true,
			expectCalls:       []string{"info"},
			expectErr:         true,
		},
		"should return error when snapshot mounts fails": {
			status:                     testStatus,
			sandbox:                    testSandbox,
			sandboxContainerdContainer: testSandboxContainer,
			snapshotMountsErr:          true,
			expectStateChange:          true,
			expectCalls:                []string{"info"},
			expectErr:                  true,
		},
		"should return error when fail to open streaming pipes": {
			status:                     testStatus,
			sandbox:                    testSandbox,
			sandboxContainerdContainer: testSandboxContainer,
			prepareFIFOErr:             errors.New("open error"),
			expectStateChange:          true,
			expectCalls:                []string{"info"},
			expectErr:                  true,
		},
		"should return error when fail to create container": {
			status:                     testStatus,
			sandbox:                    testSandbox,
			sandboxContainerdContainer: testSandboxContainer,
			createContainerErr:         errors.New("create error"),
			expectStateChange:          true,
			expectCalls:                []string{"info", "create"},
			expectErr:                  true,
		},
		"should return error when fail to start container": {
			status:                     testStatus,
			sandbox:                    testSandbox,
			sandboxContainerdContainer: testSandboxContainer,
			startContainerErr:          errors.New("start error"),
			expectStateChange:          true,
			// cleanup the containerd task.
			expectCalls: []string{"info", "create", "start", "delete"},
			expectErr:   true,
		},
		"should be able to start container successfully": {
			status:                     testStatus,
			sandbox:                    testSandbox,
			sandboxContainerdContainer: testSandboxContainer,
			expectStateChange:          true,
			expectCalls:                []string{"info", "create", "start"},
			expectErr:                  false,
		},
	} {
		t.Logf("TestCase %q", desc)
		c := newTestCRIContainerdService()
		fake := c.taskService.(*servertesting.FakeExecutionClient)
		fakeOS := c.os.(*ostesting.FakeOS)
		fakeSnapshotClient := WithFakeSnapshotClient(c)
		if test.status != nil {
			cntr, err := containerstore.NewContainer(
				testMetadata,
				*test.status,
			)
			assert.NoError(t, err)
			assert.NoError(t, c.containerStore.Add(cntr))
		}
		if test.sandbox != nil {
			assert.NoError(t, c.sandboxStore.Add(*test.sandbox))
		}
		if test.sandboxContainerdContainer != nil {
			fake.SetFakeTasks([]task.Task{*test.sandboxContainerdContainer})
		}
		if !test.snapshotMountsErr {
			fakeSnapshotClient.SetFakeMounts(testID, testMounts)
		}
		// TODO(random-liu): Test behavior with different streaming config.
		fakeOS.OpenFifoFn = func(context.Context, string, int, os.FileMode) (io.ReadWriteCloser, error) {
			return nopReadWriteCloser{}, test.prepareFIFOErr
		}
		if test.createContainerErr != nil {
			fake.InjectError("create", test.createContainerErr)
		}
		if test.startContainerErr != nil {
			fake.InjectError("start", test.startContainerErr)
		}
		resp, err := c.StartContainer(context.Background(), &runtime.StartContainerRequest{
			ContainerId: testID,
		})
		// Check containerd functions called.
		assert.Equal(t, test.expectCalls, fake.GetCalledNames())
		// Check results returned.
		if test.expectErr {
			assert.Error(t, err)
			assert.Nil(t, resp)
		} else {
			assert.NoError(t, err)
			assert.NotNil(t, resp)
		}
		// Skip following validation if no container is injected initially.
		if test.status == nil {
			continue
		}
		// Check container state.
		cntr, err := c.containerStore.Get(testID)
		assert.NoError(t, err)
		status := cntr.Status.Get()
		if !test.expectStateChange {
			assert.Equal(t, testMetadata, cntr.Metadata)
			assert.Equal(t, *test.status, status)
			continue
		}
		if test.expectErr {
			t.Logf("container state should be in exited state when fail to start")
			assert.Equal(t, runtime.ContainerState_CONTAINER_EXITED, status.State())
			assert.Zero(t, status.Pid)
			assert.EqualValues(t, errorStartExitCode, status.ExitCode)
			assert.Equal(t, errorStartReason, status.Reason)
			assert.NotEmpty(t, status.Message)
			_, err := fake.Info(context.Background(), &execution.InfoRequest{ContainerID: testID})
			assert.True(t, isContainerdGRPCNotFoundError(err),
				"containerd task should be cleaned up when fail to start")
			continue
		}
		t.Logf("container state should be running when start successfully")
		assert.Equal(t, runtime.ContainerState_CONTAINER_RUNNING, status.State())
		info, err := fake.Info(context.Background(), &execution.InfoRequest{ContainerID: testID})
		assert.NoError(t, err)
		pid := info.Task.Pid
		assert.Equal(t, pid, status.Pid)
		assert.Equal(t, task.StatusRunning, info.Task.Status)
		// Check runtime spec
		calls := fake.GetCalledDetails()
		createOpts, ok := calls[1].Argument.(*execution.CreateRequest)
		assert.True(t, ok, "2nd call should be create")
		_, stdout, stderr := getStreamingPipes(getContainerRootDir(c.rootDir, testID))
		assert.Equal(t, &execution.CreateRequest{
			ContainerID: testID,
			Rootfs:      testMounts,
			// TODO(random-liu): Test streaming configurations.
			Stdout: stdout,
			Stderr: stderr,
		}, createOpts, "create options should be correct")
	}
}
