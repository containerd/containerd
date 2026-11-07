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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	eventtypes "github.com/containerd/containerd/api/events"
	tasks "github.com/containerd/containerd/api/services/tasks/v1"
	"github.com/containerd/containerd/api/types/task"
	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/containers"
	criio "github.com/containerd/containerd/v2/internal/cri/io"
	containerstore "github.com/containerd/containerd/v2/internal/cri/store/container"
	imagestore "github.com/containerd/containerd/v2/internal/cri/store/image"
	sandboxstore "github.com/containerd/containerd/v2/internal/cri/store/sandbox"
	"github.com/containerd/containerd/v2/internal/eventq"
	"github.com/containerd/containerd/v2/pkg/cio"
	"github.com/containerd/containerd/v2/pkg/protobuf"
	"github.com/containerd/errdefs"
	"github.com/containerd/errdefs/pkg/errgrpc"
	"github.com/containerd/typeurl/v2"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
)

type exitMonitorTasksClient struct {
	tasks.TasksClient
	mu          sync.Mutex
	status      task.Status
	deleteCalls atomic.Int32
	killCalls   atomic.Int32
	deleteErr   error
}

func (c *exitMonitorTasksClient) Get(context.Context, *tasks.GetRequest, ...grpc.CallOption) (*tasks.GetResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return &tasks.GetResponse{
		Process: &task.Process{
			Status: c.status,
			Pid:    1001,
		},
	}, nil
}

func (c *exitMonitorTasksClient) Wait(context.Context, *tasks.WaitRequest, ...grpc.CallOption) (*tasks.WaitResponse, error) {
	return &tasks.WaitResponse{
		ExitStatus: 0,
		ExitedAt:   protobuf.ToTimestamp(time.Now()),
	}, nil
}

func (c *exitMonitorTasksClient) Delete(context.Context, *tasks.DeleteTaskRequest, ...grpc.CallOption) (*tasks.DeleteResponse, error) {
	c.deleteCalls.Add(1)
	if c.deleteErr != nil {
		return nil, c.deleteErr
	}
	return &tasks.DeleteResponse{
		ExitStatus: 0,
		ExitedAt:   protobuf.ToTimestamp(time.Now()),
	}, nil
}

func (c *exitMonitorTasksClient) Kill(context.Context, *tasks.KillRequest, ...grpc.CallOption) (*emptypb.Empty, error) {
	c.killCalls.Add(1)
	c.mu.Lock()
	c.status = task.Status_STOPPED
	c.mu.Unlock()
	return &emptypb.Empty{}, nil
}

func (c *exitMonitorTasksClient) Metrics(context.Context, *tasks.MetricsRequest, ...grpc.CallOption) (*tasks.MetricsResponse, error) {
	return nil, errdefs.ErrNotFound
}

type testExitImageService struct {
	fakeImageService
}

func (s *testExitImageService) GetImage(id string) (imagestore.Image, error) {
	return imagestore.Image{}, errdefs.ErrNotFound
}

type eventContainersStore struct {
	containers.Store
}

func (f *eventContainersStore) Get(context.Context, string) (containers.Container, error) {
	specAny, err := typeurl.MarshalAny(&runtimespec.Spec{
		Process: &runtimespec.Process{
			User: runtimespec.User{},
		},
	})
	if err != nil {
		return containers.Container{}, err
	}
	return containers.Container{
		Spec: specAny,
	}, nil
}

func setupTestContainerForExit(t *testing.T, c *criService, containerID, sandboxID string) containerstore.Container {
	ctx := context.Background()
	c.ImageService = &testExitImageService{}
	c.containerEventsQ = eventq.New[*runtime.ContainerEventResponse](time.Minute, func(*runtime.ContainerEventResponse) {})

	sbConfig := &runtime.PodSandboxConfig{
		Linux: &runtime.LinuxPodSandboxConfig{
			SecurityContext: &runtime.LinuxSandboxSecurityContext{
				NamespaceOptions: &runtime.NamespaceOption{
					Network: runtime.NamespaceMode_NODE,
				},
			},
		},
	}
	require.NoError(t, c.sandboxStore.Add(sandboxstore.NewSandbox(
		sandboxstore.Metadata{ID: sandboxID, Config: sbConfig},
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
		containerstore.WithFakeStatus(containerstore.Status{
			CreatedAt: time.Now().UnixNano(),
			StartedAt: time.Now().UnixNano(),
		}),
	)
	require.NoError(t, err)
	require.NoError(t, c.containerStore.Add(container))
	return container
}

func TestHandleContainerExit_CleanExitNoKill(t *testing.T) {
	const (
		containerID = "clean-exit-container"
		sandboxID   = "clean-exit-sandbox"
	)
	mockClient := &exitMonitorTasksClient{
		status: task.Status_STOPPED,
	}
	c := newTestCRIServiceWithClient(
		mockClient,
		containerd.WithContainerStore(&eventContainersStore{}),
	)

	container := setupTestContainerForExit(t, c, containerID, sandboxID)

	exitEvent := &eventtypes.TaskExit{
		ContainerID: containerID,
		ID:          containerID,
		Pid:         1001,
		ExitStatus:  0,
		ExitedAt:    protobuf.ToTimestamp(time.Now()),
	}

	err := c.handleContainerExit(context.Background(), exitEvent, container, sandboxID)
	require.NoError(t, err)

	// Clean exit must not invoke Kill gRPC.
	assert.Equal(t, int32(0), mockClient.killCalls.Load(), "redundant kill should not be invoked on clean exit")
	assert.Equal(t, int32(1), mockClient.deleteCalls.Load(), "task Delete should be invoked once")

	status := container.Status.Get()
	assert.Equal(t, runtime.ContainerState_CONTAINER_EXITED, status.State())
	assert.Equal(t, int32(0), status.ExitCode)

	select {
	case <-container.Stopped():
	default:
		t.Fatal("container.Stopped() channel should be closed")
	}
}

func TestHandleContainerExit_FallbackToWithProcessKill(t *testing.T) {
	const (
		containerID = "fallback-container"
		sandboxID   = "fallback-sandbox"
	)
	mockClient := &exitMonitorTasksClient{
		status: task.Status_RUNNING,
	}
	c := newTestCRIServiceWithClient(
		mockClient,
		containerd.WithContainerStore(&eventContainersStore{}),
	)

	container := setupTestContainerForExit(t, c, containerID, sandboxID)

	exitEvent := &eventtypes.TaskExit{
		ContainerID: containerID,
		ID:          containerID,
		Pid:         1001,
		ExitStatus:  1,
		ExitedAt:    protobuf.ToTimestamp(time.Now()),
	}

	err := c.handleContainerExit(context.Background(), exitEvent, container, sandboxID)
	require.NoError(t, err)

	// Fallback should invoke Kill gRPC to terminate lingering process.
	assert.Equal(t, int32(1), mockClient.killCalls.Load(), "fallback WithProcessKill should invoke Kill")
	assert.Equal(t, int32(1), mockClient.deleteCalls.Load(), "task Delete should be invoked")

	status := container.Status.Get()
	assert.Equal(t, runtime.ContainerState_CONTAINER_EXITED, status.State())
	assert.Equal(t, int32(1), status.ExitCode)

	select {
	case <-container.Stopped():
	default:
		t.Fatal("container.Stopped() channel should be closed")
	}
}

func TestHandleContainerExit_TaskNotFound(t *testing.T) {
	const (
		containerID = "not-found-container"
		sandboxID   = "not-found-sandbox"
	)
	mockClient := &exitMonitorTasksClient{
		status:    task.Status_STOPPED,
		deleteErr: errgrpc.ToGRPC(errdefs.ErrNotFound),
	}
	c := newTestCRIServiceWithClient(
		mockClient,
		containerd.WithContainerStore(&eventContainersStore{}),
	)

	container := setupTestContainerForExit(t, c, containerID, sandboxID)

	exitEvent := &eventtypes.TaskExit{
		ContainerID: containerID,
		ID:          containerID,
		Pid:         1001,
		ExitStatus:  0,
		ExitedAt:    protobuf.ToTimestamp(time.Now()),
	}

	err := c.handleContainerExit(context.Background(), exitEvent, container, sandboxID)
	require.NoError(t, err)

	assert.Equal(t, int32(0), mockClient.killCalls.Load(), "Kill should not be invoked")
	status := container.Status.Get()
	assert.Equal(t, runtime.ContainerState_CONTAINER_EXITED, status.State())

	select {
	case <-container.Stopped():
	default:
		t.Fatal("container.Stopped() channel should be closed")
	}
}
