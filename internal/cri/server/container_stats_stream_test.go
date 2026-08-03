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
	goruntime "runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	tasks "github.com/containerd/containerd/api/services/tasks/v1"
	"github.com/containerd/containerd/api/types"
	containerd "github.com/containerd/containerd/v2/client"
	containerstore "github.com/containerd/containerd/v2/internal/cri/store/container"
	sandboxstore "github.com/containerd/containerd/v2/internal/cri/store/sandbox"
)

// fakeTasksClient implements tasks.TasksClient for testing.
type fakeTasksClient struct {
	metricsResp *tasks.MetricsResponse
	metricsErr  error
}

func (f *fakeTasksClient) Metrics(_ context.Context, _ *tasks.MetricsRequest, _ ...grpc.CallOption) (*tasks.MetricsResponse, error) {
	if f.metricsErr != nil {
		return nil, f.metricsErr
	}
	if f.metricsResp != nil {
		return f.metricsResp, nil
	}
	return &tasks.MetricsResponse{}, nil
}

func (f *fakeTasksClient) Create(context.Context, *tasks.CreateTaskRequest, ...grpc.CallOption) (*tasks.CreateTaskResponse, error) {
	return nil, nil
}
func (f *fakeTasksClient) Start(context.Context, *tasks.StartRequest, ...grpc.CallOption) (*tasks.StartResponse, error) {
	return nil, nil
}
func (f *fakeTasksClient) Delete(context.Context, *tasks.DeleteTaskRequest, ...grpc.CallOption) (*tasks.DeleteResponse, error) {
	return nil, nil
}
func (f *fakeTasksClient) DeleteProcess(context.Context, *tasks.DeleteProcessRequest, ...grpc.CallOption) (*tasks.DeleteResponse, error) {
	return nil, nil
}
func (f *fakeTasksClient) Get(context.Context, *tasks.GetRequest, ...grpc.CallOption) (*tasks.GetResponse, error) {
	return nil, nil
}
func (f *fakeTasksClient) List(context.Context, *tasks.ListTasksRequest, ...grpc.CallOption) (*tasks.ListTasksResponse, error) {
	return nil, nil
}
func (f *fakeTasksClient) Kill(context.Context, *tasks.KillRequest, ...grpc.CallOption) (*emptypb.Empty, error) {
	return nil, nil
}
func (f *fakeTasksClient) Exec(context.Context, *tasks.ExecProcessRequest, ...grpc.CallOption) (*emptypb.Empty, error) {
	return nil, nil
}
func (f *fakeTasksClient) ResizePty(context.Context, *tasks.ResizePtyRequest, ...grpc.CallOption) (*emptypb.Empty, error) {
	return nil, nil
}
func (f *fakeTasksClient) CloseIO(context.Context, *tasks.CloseIORequest, ...grpc.CallOption) (*emptypb.Empty, error) {
	return nil, nil
}
func (f *fakeTasksClient) Pause(context.Context, *tasks.PauseTaskRequest, ...grpc.CallOption) (*emptypb.Empty, error) {
	return nil, nil
}
func (f *fakeTasksClient) Resume(context.Context, *tasks.ResumeTaskRequest, ...grpc.CallOption) (*emptypb.Empty, error) {
	return nil, nil
}
func (f *fakeTasksClient) ListPids(context.Context, *tasks.ListPidsRequest, ...grpc.CallOption) (*tasks.ListPidsResponse, error) {
	return nil, nil
}
func (f *fakeTasksClient) Checkpoint(context.Context, *tasks.CheckpointTaskRequest, ...grpc.CallOption) (*tasks.CheckpointTaskResponse, error) {
	return nil, nil
}
func (f *fakeTasksClient) Update(context.Context, *tasks.UpdateTaskRequest, ...grpc.CallOption) (*emptypb.Empty, error) {
	return nil, nil
}
func (f *fakeTasksClient) Wait(context.Context, *tasks.WaitRequest, ...grpc.CallOption) (*tasks.WaitResponse, error) {
	return nil, nil
}

// newTestCRIServiceWithClient creates a test criService with a containerd client
// backed by the given mock task service.
func newTestCRIServiceWithClient(taskSvc tasks.TasksClient) *criService {
	c := newTestCRIService()
	cli, err := containerd.New("",
		containerd.WithServices(
			containerd.WithTaskClient(taskSvc),
		),
	)
	if err != nil {
		panic(err)
	}
	c.client = cli
	return c
}

func TestStreamContainerStatsEmpty(t *testing.T) {
	if goruntime.GOOS == "darwin" {
		t.Skip("not implemented on Darwin")
	}

	c := newTestCRIServiceWithClient(&fakeTasksClient{})

	stream := newFakeStreamServer[runtime.StreamContainerStatsResponse](context.Background())
	err := c.StreamContainerStats(&runtime.StreamContainerStatsRequest{}, stream)
	assert.NoError(t, err)
	assert.Empty(t, stream.sent, "no messages should be sent for empty store")
}

func TestStreamContainerStatsWithMetrics(t *testing.T) {
	if goruntime.GOOS == "darwin" {
		t.Skip("not implemented on Darwin")
	}

	createdAt := time.Now().UnixNano()
	sb := sandboxstore.NewSandbox(
		sandboxstore.Metadata{
			ID:             "s-1",
			Name:           "sandbox-1",
			Config:         &runtime.PodSandboxConfig{Metadata: &runtime.PodSandboxMetadata{Name: "pod-1"}},
			RuntimeHandler: "runc",
		},
		sandboxstore.Status{
			State: sandboxstore.StateReady,
		},
	)
	sb.Sandboxer = "shim"
	container, err := containerstore.NewContainer(
		containerstore.Metadata{
			ID:        "c-1",
			Name:      "container-1",
			SandboxID: "s-1",
			Config: &runtime.ContainerConfig{
				Metadata: &runtime.ContainerMetadata{Name: "container-1"},
			},
		},
		containerstore.WithFakeStatus(containerstore.Status{
			CreatedAt: createdAt,
			StartedAt: createdAt,
		}),
	)
	require.NoError(t, err)

	taskSvc := &fakeTasksClient{
		metricsResp: &tasks.MetricsResponse{
			Metrics: []*types.Metric{
				{
					ID:   "c-1",
					Data: platformBasedMetricsData(t),
				},
			},
		},
	}

	c := newTestCRIServiceWithClient(taskSvc)
	require.NoError(t, c.sandboxStore.Add(sb))
	require.NoError(t, c.containerStore.Add(container))

	stream := newFakeStreamServer[runtime.StreamContainerStatsResponse](context.Background())
	err = c.StreamContainerStats(&runtime.StreamContainerStatsRequest{}, stream)
	assert.NoError(t, err)
	require.NotEmpty(t, stream.sent)

	var stats []*runtime.ContainerStats
	for _, resp := range stream.sent {
		stats = append(stats, resp.ContainerStats...)
	}
	require.Len(t, stats, 1)
	assert.Equal(t, "c-1", stats[0].Attributes.Id)
}

func TestStreamContainerStatsWithFilter(t *testing.T) {
	if goruntime.GOOS == "darwin" {
		t.Skip("not implemented on Darwin")
	}

	createdAt := time.Now().UnixNano()
	sb := sandboxstore.NewSandbox(
		sandboxstore.Metadata{
			ID:             "s-1",
			Name:           "sandbox-1",
			Config:         &runtime.PodSandboxConfig{Metadata: &runtime.PodSandboxMetadata{Name: "pod-1"}},
			RuntimeHandler: "runc",
		},
		sandboxstore.Status{
			State: sandboxstore.StateReady,
		},
	)
	sb.Sandboxer = "shim"

	containers := []containerstore.Metadata{
		{
			ID:        "c-1",
			Name:      "container-1",
			SandboxID: "s-1",
			Config: &runtime.ContainerConfig{
				Metadata: &runtime.ContainerMetadata{Name: "container-1"},
				Labels:   map[string]string{"app": "web"},
			},
		},
		{
			ID:        "c-2",
			Name:      "container-2",
			SandboxID: "s-1",
			Config: &runtime.ContainerConfig{
				Metadata: &runtime.ContainerMetadata{Name: "container-2"},
				Labels:   map[string]string{"app": "db"},
			},
		},
	}

	taskSvc := &fakeTasksClient{
		metricsResp: &tasks.MetricsResponse{
			Metrics: []*types.Metric{
				{ID: "c-1", Data: platformBasedMetricsData(t)},
			},
		},
	}

	c := newTestCRIServiceWithClient(taskSvc)
	require.NoError(t, c.sandboxStore.Add(sb))
	for _, meta := range containers {
		cntr, err := containerstore.NewContainer(meta, containerstore.WithFakeStatus(containerstore.Status{
			CreatedAt: createdAt,
			StartedAt: createdAt,
		}))
		require.NoError(t, err)
		require.NoError(t, c.containerStore.Add(cntr))
	}

	// Filter by container ID — should only return c-1
	stream := newFakeStreamServer[runtime.StreamContainerStatsResponse](context.Background())
	err := c.StreamContainerStats(&runtime.StreamContainerStatsRequest{
		Filter: &runtime.ContainerStatsFilter{Id: "c-1"},
	}, stream)
	assert.NoError(t, err)

	var stats []*runtime.ContainerStats
	for _, resp := range stream.sent {
		stats = append(stats, resp.ContainerStats...)
	}
	require.Len(t, stats, 1)
	assert.Equal(t, "c-1", stats[0].Attributes.Id)
}

func TestStreamContainerStatsMetricsError(t *testing.T) {
	if goruntime.GOOS == "darwin" {
		t.Skip("not implemented on Darwin")
	}

	taskSvc := &fakeTasksClient{
		metricsErr: assert.AnError,
	}

	c := newTestCRIServiceWithClient(taskSvc)

	stream := newFakeStreamServer[runtime.StreamContainerStatsResponse](context.Background())
	err := c.StreamContainerStats(&runtime.StreamContainerStatsRequest{}, stream)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to fetch containers and stats")
}
