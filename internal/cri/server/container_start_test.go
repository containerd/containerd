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

	"github.com/containerd/typeurl/v2"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

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

func (f *fakeContainersStore) Get(_ context.Context, id string) (containers.Container, error) {
	// Container events read the runtime spec, so it must be present.
	spec, err := typeurl.MarshalAny(&runtimespec.Spec{})
	if err != nil {
		return containers.Container{}, err
	}
	return containers.Container{ID: id, Spec: spec}, nil
}

func (f *fakeContainersStore) Delete(context.Context, string) error { return nil }

// newTestCRIServiceWithEvents creates a test service backed by the given task
// service and a container store, with what is needed to emit container events.
func newTestCRIServiceWithEvents(t *testing.T, taskSvc tasks.TasksClient) *criService {
	t.Helper()
	c := newTestCRIServiceWithClient(taskSvc, containerd.WithContainerStore(&fakeContainersStore{}))
	is, err := imagestore.NewFakeStore(nil)
	require.NoError(t, err)
	c.ImageService = &fakeImageService{imageStore: is}
	c.containerEventsQ = eventq.New[*runtime.ContainerEventResponse](time.Minute, func(*runtime.ContainerEventResponse) {})
	t.Cleanup(c.containerEventsQ.Shutdown)
	return c
}

// addTestContainer adds a ready sandbox and a container with the given status
// to the service's stores.
func addTestContainer(t *testing.T, c *criService, containerID, sandboxID string, st containerstore.Status) containerstore.Container {
	t.Helper()
	// A host network sandbox, so that container events do not need the netns
	// that a real pod network sandbox would have.
	sandboxConfig := &runtime.PodSandboxConfig{
		Linux: &runtime.LinuxPodSandboxConfig{SecurityContext: &runtime.LinuxSandboxSecurityContext{
			NamespaceOptions: &runtime.NamespaceOption{Network: runtime.NamespaceMode_NODE},
		}},
		Windows: &runtime.WindowsPodSandboxConfig{SecurityContext: &runtime.WindowsSandboxSecurityContext{HostProcess: true}},
	}
	require.NoError(t, c.sandboxStore.Add(sandboxstore.NewSandbox(
		sandboxstore.Metadata{ID: sandboxID, Config: sandboxConfig},
		sandboxstore.Status{State: sandboxstore.StateReady},
	)))

	cntr, err := c.client.LoadContainer(t.Context(), containerID)
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
		containerstore.WithFakeStatus(st),
	)
	require.NoError(t, err)
	require.NoError(t, c.containerStore.Add(container))
	return container
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

			container := addTestContainer(t, c, containerID, sandboxID, containerstore.Status{CreatedAt: time.Now().UnixNano()})

			_, err := c.StartContainer(ctx, &runtime.StartContainerRequest{ContainerId: containerID})
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

// leakedTaskClient is a task service whose Create fails after the runtime
// created the task, so what it created survives the error. A leftover task is
// CREATED with a pid, and only a Kill makes it STOPPED and deletable. A
// retained entry is invisible to Get (NotFound) and only the task service
// Delete can remove it. While wedged, Kill and Delete fail the way they do on
// an unresponsive shim.
type leakedTaskClient struct {
	tasks.TasksClient
	state    task.Status
	retained bool
	wedged   bool
	// killed is closed once a Kill is accepted.
	killed chan struct{}
}

func newLeakedTaskClient(created, retained, wedged bool) *leakedTaskClient {
	f := &leakedTaskClient{retained: retained, wedged: wedged, killed: make(chan struct{})}
	if created {
		f.state = task.Status_CREATED
	}
	return f
}

func (f *leakedTaskClient) hasRecord() bool { return f.state != task.Status_UNKNOWN || f.retained }

func (f *leakedTaskClient) unwedge() { f.wedged = false }

func (f *leakedTaskClient) Create(context.Context, *tasks.CreateTaskRequest, ...grpc.CallOption) (*tasks.CreateTaskResponse, error) {
	return nil, errors.New("failed to get task pid")
}

func (f *leakedTaskClient) Get(_ context.Context, req *tasks.GetRequest, _ ...grpc.CallOption) (*tasks.GetResponse, error) {
	if f.state == task.Status_UNKNOWN {
		return nil, status.Error(codes.NotFound, "task not found")
	}
	return &tasks.GetResponse{Process: &task.Process{ID: req.ContainerID, Pid: 42, Status: f.state}}, nil
}

// Wait returns once Kill was accepted, like a shim reaping the init exit. It
// is also called from task.Wait's background goroutine.
func (f *leakedTaskClient) Wait(ctx context.Context, _ *tasks.WaitRequest, _ ...grpc.CallOption) (*tasks.WaitResponse, error) {
	select {
	case <-f.killed:
		return &tasks.WaitResponse{}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (f *leakedTaskClient) Kill(context.Context, *tasks.KillRequest, ...grpc.CallOption) (*emptypb.Empty, error) {
	if f.wedged {
		return nil, status.Error(codes.DeadlineExceeded, "kill timed out")
	}
	if f.state != task.Status_UNKNOWN {
		f.state = task.Status_STOPPED
	}
	select {
	case <-f.killed:
	default:
		close(f.killed)
	}
	return &emptypb.Empty{}, nil
}

func (f *leakedTaskClient) Delete(context.Context, *tasks.DeleteTaskRequest, ...grpc.CallOption) (*tasks.DeleteResponse, error) {
	if f.wedged {
		return nil, status.Error(codes.DeadlineExceeded, "delete timed out")
	}
	switch f.state {
	case task.Status_UNKNOWN:
		if !f.retained {
			return nil, status.Error(codes.NotFound, "task not found")
		}
		f.retained = false
	case task.Status_STOPPED:
		f.state = task.Status_UNKNOWN
	default:
		return nil, status.Error(codes.FailedPrecondition, "cannot delete running task")
	}
	return &tasks.DeleteResponse{}, nil
}

// TestStartContainerNewTaskFailureState verifies that when creating the task
// fails and the task record may still exist, the container is left UNKNOWN
// unless the record is confirmed gone, so RemoveContainer can reap it once the
// shim recovers instead of failing forever on "cannot delete running task".
func TestStartContainerNewTaskFailureState(t *testing.T) {
	const (
		containerID = "test-container"
		sandboxID   = "test-sandbox"
	)

	for _, test := range []struct {
		desc                 string
		created              bool // task visible to the client, CREATED
		retained             bool // shim entry only the task service can delete
		wedged               bool
		expectedState        runtime.ContainerState
		wantRecordAfterStart bool
	}{
		{
			desc:          "nothing left behind",
			expectedState: runtime.ContainerState_CONTAINER_EXITED,
		},
		{
			desc:          "leaked task deleted",
			created:       true,
			expectedState: runtime.ContainerState_CONTAINER_EXITED,
		},
		{
			desc:                 "leaked task cannot be killed or deleted",
			created:              true,
			wedged:               true,
			expectedState:        runtime.ContainerState_CONTAINER_UNKNOWN,
			wantRecordAfterStart: true,
		},
		{
			desc:          "retained shim entry deleted through the task service",
			retained:      true,
			expectedState: runtime.ContainerState_CONTAINER_EXITED,
		},
		{
			desc:                 "retained shim entry cannot be deleted through the task service",
			retained:             true,
			wedged:               true,
			expectedState:        runtime.ContainerState_CONTAINER_UNKNOWN,
			wantRecordAfterStart: true,
		},
	} {
		t.Run(test.desc, func(t *testing.T) {
			ctx := t.Context()
			shim := newLeakedTaskClient(test.created, test.retained, test.wedged)
			c := newTestCRIServiceWithEvents(t, shim)
			container := addTestContainer(t, c, containerID, sandboxID, containerstore.Status{CreatedAt: time.Now().UnixNano()})

			_, err := c.StartContainer(ctx, &runtime.StartContainerRequest{ContainerId: containerID})
			require.ErrorContains(t, err, "failed to create containerd task")
			assert.Equal(t, test.expectedState, container.Status.Get().State())
			assert.Equal(t, test.wantRecordAfterStart, shim.hasRecord())

			shim.unwedge()
			_, err = c.RemoveContainer(ctx, &runtime.RemoveContainerRequest{ContainerId: containerID})
			require.NoError(t, err)
			assert.False(t, shim.hasRecord())
		})
	}
}
