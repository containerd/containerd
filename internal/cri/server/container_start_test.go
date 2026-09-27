/*
   Copyright The containerd Authors.

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
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	tasks "github.com/containerd/containerd/api/services/tasks/v1"
	"github.com/containerd/containerd/api/types/task"
	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/containers"
	criio "github.com/containerd/containerd/v2/internal/cri/io"
	containerstore "github.com/containerd/containerd/v2/internal/cri/store/container"
	sandboxstore "github.com/containerd/containerd/v2/internal/cri/store/sandbox"
	"github.com/containerd/containerd/v2/pkg/cio"
)

// TestSetContainerStarting tests setContainerStarting sets removing
// state correctly.
func TestSetContainerStarting(t *testing.T) {
	testID := "test-id"
	for _, test := range []struct {
		desc      string
		status    containerstore.Status
		expectErr bool
	}{
		{
			desc: "should not return error when container is in created state",
			status: containerstore.Status{
				CreatedAt: time.Now().UnixNano(),
			},
			expectErr: false,
		},
		{
			desc: "should return error when container is in running state",
			status: containerstore.Status{
				CreatedAt: time.Now().UnixNano(),
				StartedAt: time.Now().UnixNano(),
			},
			expectErr: true,
		},
		{
			desc: "should return error when container is in exited state",
			status: containerstore.Status{
				CreatedAt:  time.Now().UnixNano(),
				StartedAt:  time.Now().UnixNano(),
				FinishedAt: time.Now().UnixNano(),
			},
			expectErr: true,
		},
		{
			desc: "should return error when container is in unknown state",
			status: containerstore.Status{
				CreatedAt:  0,
				StartedAt:  0,
				FinishedAt: 0,
			},
			expectErr: true,
		},
		{
			desc: "should return error when container is in starting state",
			status: containerstore.Status{
				CreatedAt: time.Now().UnixNano(),
				Starting:  true,
			},
			expectErr: true,
		},
		{
			desc: "should return error when container is in removing state",
			status: containerstore.Status{
				CreatedAt: time.Now().UnixNano(),
				Removing:  true,
			},
			expectErr: true,
		},
	} {
		t.Run(test.desc, func(t *testing.T) {
			container, err := containerstore.NewContainer(
				containerstore.Metadata{ID: testID},
				containerstore.WithFakeStatus(test.status),
			)
			assert.NoError(t, err)
			err = setContainerStarting(container)
			if test.expectErr {
				assert.Error(t, err)
				assert.Equal(t, test.status, container.Status.Get(), "metadata should not be updated")
			} else {
				assert.NoError(t, err)
				assert.True(t, container.Status.Get().Starting, "starting should be set")
				assert.NoError(t, resetContainerStarting(container))
				assert.False(t, container.Status.Get().Starting, "starting should be reset")
			}
		})
	}
}

// startFailureTasksClient is a task service whose Start always fails, with a
// configurable outcome for the cleanup Delete issued by StartContainer.
type startFailureTasksClient struct {
	tasks.TasksClient
	deleteErr error
}

func (f *startFailureTasksClient) Create(context.Context, *tasks.CreateTaskRequest, ...grpc.CallOption) (*tasks.CreateTaskResponse, error) {
	return &tasks.CreateTaskResponse{}, nil
}

// Wait is called from task.Wait's background goroutine.
func (f *startFailureTasksClient) Wait(context.Context, *tasks.WaitRequest, ...grpc.CallOption) (*tasks.WaitResponse, error) {
	return &tasks.WaitResponse{}, nil
}

func (f *startFailureTasksClient) Start(context.Context, *tasks.StartRequest, ...grpc.CallOption) (*tasks.StartResponse, error) {
	return nil, errors.New("start failed")
}

func (f *startFailureTasksClient) Get(context.Context, *tasks.GetRequest, ...grpc.CallOption) (*tasks.GetResponse, error) {
	return &tasks.GetResponse{Process: &task.Process{Status: task.Status_STOPPED}}, nil
}

func (f *startFailureTasksClient) Delete(context.Context, *tasks.DeleteTaskRequest, ...grpc.CallOption) (*tasks.DeleteResponse, error) {
	if f.deleteErr != nil {
		return nil, f.deleteErr
	}
	return &tasks.DeleteResponse{}, nil
}

type fakeContainersStore struct {
	containers.Store
}

func (f *fakeContainersStore) Get(context.Context, string) (containers.Container, error) {
	return containers.Container{}, nil
}

// TestStartContainerFailureState verifies the state StartContainer records when
// the task fails to start: a container whose task was cleaned up is EXITED, but
// one whose task delete failed (e.g. timed out on a wedged shim) must be left
// UNKNOWN, so RemoveContainer force-stops and reaps the leftover task instead
// of failing forever on "cannot delete running task".
func TestStartContainerFailureState(t *testing.T) {
	const (
		containerID = "test-container"
		sandboxID   = "test-sandbox"
	)

	for _, test := range []struct {
		desc          string
		deleteErr     error
		expectedState runtime.ContainerState
	}{
		{
			desc:          "task deleted",
			expectedState: runtime.ContainerState_CONTAINER_EXITED,
		},
		{
			desc:          "task delete failed",
			deleteErr:     context.DeadlineExceeded,
			expectedState: runtime.ContainerState_CONTAINER_UNKNOWN,
		},
	} {
		t.Run(test.desc, func(t *testing.T) {
			ctx := context.Background()
			c := newTestCRIServiceWithClient(
				&startFailureTasksClient{deleteErr: test.deleteErr},
				containerd.WithContainerStore(&fakeContainersStore{}),
			)

			require.NoError(t, c.sandboxStore.Add(sandboxstore.NewSandbox(
				sandboxstore.Metadata{ID: sandboxID, Config: &runtime.PodSandboxConfig{}},
				sandboxstore.Status{State: sandboxstore.StateReady},
			)))

			cntr, err := c.client.LoadContainer(ctx, containerID)
			require.NoError(t, err)
			containerIO, err := criio.NewContainerIO(containerID, criio.WithFIFOs(cio.NewFIFOSet(cio.Config{}, nil)))
			require.NoError(t, err)
			container, err := containerstore.NewContainer(
				containerstore.Metadata{
					ID:        containerID,
					SandboxID: sandboxID,
					Config:    &runtime.ContainerConfig{},
				},
				containerstore.WithContainer(cntr),
				containerstore.WithContainerIO(containerIO),
				containerstore.WithFakeStatus(containerstore.Status{CreatedAt: time.Now().UnixNano()}),
			)
			require.NoError(t, err)
			require.NoError(t, c.containerStore.Add(container))

			_, err = c.StartContainer(ctx, &runtime.StartContainerRequest{ContainerId: containerID})
			require.ErrorContains(t, err, "start failed")

			status := container.Status.Get()
			assert.Equal(t, test.expectedState, status.State())
			assert.False(t, status.Starting)
			assert.NotZero(t, status.FinishedAt)
			assert.Equal(t, int32(errorStartExitCode), status.ExitCode)
			assert.Equal(t, errorStartReason, status.Reason)
			assert.Contains(t, status.Message, "start failed")
		})
	}
}
