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
	"github.com/stretchr/testify/require"
	"golang.org/x/net/context"
	runtime "k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1"

	"github.com/kubernetes-incubator/cri-containerd/pkg/metadata"
	ostesting "github.com/kubernetes-incubator/cri-containerd/pkg/os/testing"
	servertesting "github.com/kubernetes-incubator/cri-containerd/pkg/server/testing"
)

func TestStartContainer(t *testing.T) {
	testID := "test-id"
	testSandboxID := "test-sandbox-id"
	testMetadata := &metadata.ContainerMetadata{
		ID:        testID,
		Name:      "test-name",
		SandboxID: testSandboxID,
		CreatedAt: time.Now().UnixNano(),
	}
	testSandboxMetadata := &metadata.SandboxMetadata{
		ID:   testSandboxID,
		Name: "test-sandbox-name",
	}
	testSandboxContainer := &task.Task{
		ID:     testSandboxID,
		Pid:    uint32(4321),
		Status: task.StatusRunning,
	}
	testMounts := []*mount.Mount{{Type: "bind", Source: "test-source"}}
	for desc, test := range map[string]struct {
		containerMetadata          *metadata.ContainerMetadata
		sandboxMetadata            *metadata.SandboxMetadata
		sandboxContainerdContainer *task.Task
		snapshotMountsErr          bool
		prepareFIFOErr             error
		createContainerErr         error
		startContainerErr          error
		expectStateChange          bool
		expectCalls                []string
		expectErr                  bool
	}{
		"should return error when container metadata does not exist": {
			containerMetadata:          nil,
			sandboxMetadata:            testSandboxMetadata,
			sandboxContainerdContainer: testSandboxContainer,
			expectCalls:                []string{},
			expectErr:                  true,
		},
		"should return error when container is not in created state": {
			containerMetadata: &metadata.ContainerMetadata{
				ID:        testID,
				Name:      "test-name",
				SandboxID: testSandboxID,
				CreatedAt: time.Now().UnixNano(),
				StartedAt: time.Now().UnixNano(),
			},
			sandboxMetadata:            testSandboxMetadata,
			sandboxContainerdContainer: testSandboxContainer,
			expectCalls:                []string{},
			expectErr:                  true,
		},
		"should return error when container is in removing state": {
			containerMetadata: &metadata.ContainerMetadata{
				ID:        testID,
				Name:      "test-name",
				SandboxID: testSandboxID,
				CreatedAt: time.Now().UnixNano(),
				Removing:  true,
			},
			sandboxMetadata:            testSandboxMetadata,
			sandboxContainerdContainer: testSandboxContainer,
			expectCalls:                []string{},
			expectErr:                  true,
		},
		"should return error when sandbox does not exist": {
			containerMetadata:          testMetadata,
			sandboxMetadata:            nil,
			sandboxContainerdContainer: testSandboxContainer,
			expectStateChange:          true,
			expectCalls:                []string{},
			expectErr:                  true,
		},
		"should return error when sandbox is not running": {
			containerMetadata: testMetadata,
			sandboxMetadata:   testSandboxMetadata,
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
			containerMetadata:          testMetadata,
			sandboxMetadata:            testSandboxMetadata,
			sandboxContainerdContainer: testSandboxContainer,
			snapshotMountsErr:          true,
			expectStateChange:          true,
			expectCalls:                []string{"info"},
			expectErr:                  true,
		},
		"should return error when fail to open streaming pipes": {
			containerMetadata:          testMetadata,
			sandboxMetadata:            testSandboxMetadata,
			sandboxContainerdContainer: testSandboxContainer,
			prepareFIFOErr:             errors.New("open error"),
			expectStateChange:          true,
			expectCalls:                []string{"info"},
			expectErr:                  true,
		},
		"should return error when fail to create container": {
			containerMetadata:          testMetadata,
			sandboxMetadata:            testSandboxMetadata,
			sandboxContainerdContainer: testSandboxContainer,
			createContainerErr:         errors.New("create error"),
			expectStateChange:          true,
			expectCalls:                []string{"info", "create"},
			expectErr:                  true,
		},
		"should return error when fail to start container": {
			containerMetadata:          testMetadata,
			sandboxMetadata:            testSandboxMetadata,
			sandboxContainerdContainer: testSandboxContainer,
			startContainerErr:          errors.New("start error"),
			expectStateChange:          true,
			// cleanup the containerd task.
			expectCalls: []string{"info", "create", "start", "delete"},
			expectErr:   true,
		},
		"should be able to start container successfully": {
			containerMetadata:          testMetadata,
			sandboxMetadata:            testSandboxMetadata,
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
		if test.containerMetadata != nil {
			assert.NoError(t, c.containerStore.Create(*test.containerMetadata))
		}
		if test.sandboxMetadata != nil {
			assert.NoError(t, c.sandboxStore.Create(*test.sandboxMetadata))
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
		// Check container state.
		meta, err := c.containerStore.Get(testID)
		if !test.expectStateChange {
			// Do not check the error, because container may not exist
			// in the test case.
			assert.Equal(t, meta, test.containerMetadata)
			continue
		}
		assert.NoError(t, err)
		require.NotNil(t, meta)
		if test.expectErr {
			t.Logf("container state should be in exited state when fail to start")
			assert.Equal(t, runtime.ContainerState_CONTAINER_EXITED, meta.State())
			assert.Zero(t, meta.Pid)
			assert.EqualValues(t, errorStartExitCode, meta.ExitCode)
			assert.Equal(t, errorStartReason, meta.Reason)
			assert.NotEmpty(t, meta.Message)
			_, err := fake.Info(context.Background(), &execution.InfoRequest{ContainerID: testID})
			assert.True(t, isContainerdGRPCNotFoundError(err),
				"containerd task should be cleaned up when fail to start")
			continue
		}
		t.Logf("container state should be running when start successfully")
		assert.Equal(t, runtime.ContainerState_CONTAINER_RUNNING, meta.State())
		info, err := fake.Info(context.Background(), &execution.InfoRequest{ContainerID: testID})
		assert.NoError(t, err)
		pid := info.Task.Pid
		assert.Equal(t, pid, meta.Pid)
		assert.Equal(t, task.StatusRunning, info.Task.Status)
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
