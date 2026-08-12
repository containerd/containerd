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
	"fmt"
	"syscall"
	"testing"
	"time"

	"github.com/containerd/errdefs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	eventtypes "github.com/containerd/containerd/api/events"
	tasks "github.com/containerd/containerd/api/services/tasks/v1"
	apitypes "github.com/containerd/containerd/api/types"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/containers"
	"github.com/containerd/containerd/v2/internal/cri/nri"
	containerstore "github.com/containerd/containerd/v2/internal/cri/store/container"
	"github.com/containerd/containerd/v2/internal/eventq"
	"github.com/containerd/containerd/v2/pkg/oci"
	"github.com/containerd/typeurl/v2"
	specs "github.com/opencontainers/runtime-spec/specs-go"

	containerdcio "github.com/containerd/containerd/v2/pkg/cio"
)

// fakeTask implements containerd.Task for testing handleContainerExit.
// Only Delete() has meaningful behavior; all other methods are no-ops.
type fakeTask struct {
	id        string
	deleteErr error
}

func (t *fakeTask) ID() string                                                                   { return t.id }
func (t *fakeTask) Pid() uint32                                                                  { return 0 }
func (t *fakeTask) Start(context.Context) error                                                  { return nil }
func (t *fakeTask) Kill(context.Context, syscall.Signal, ...containerd.KillOpts) error            { return nil }
func (t *fakeTask) Wait(context.Context) (<-chan containerd.ExitStatus, error)                    { return nil, nil }
func (t *fakeTask) CloseIO(context.Context, ...containerd.IOCloserOpts) error                    { return nil }
func (t *fakeTask) Resize(_ context.Context, _, _ uint32) error                                  { return nil }
func (t *fakeTask) IO() containerdcio.IO                                                         { return nil }
func (t *fakeTask) Status(context.Context) (containerd.Status, error)                            { return containerd.Status{}, nil }
func (t *fakeTask) Pause(context.Context) error                                                  { return nil }
func (t *fakeTask) Resume(context.Context) error                                                 { return nil }
func (t *fakeTask) Exec(context.Context, string, *specs.Process, containerdcio.Creator) (containerd.Process, error) {
	return nil, nil
}
func (t *fakeTask) Pids(context.Context) ([]containerd.ProcessInfo, error)                       { return nil, nil }
func (t *fakeTask) Checkpoint(context.Context, ...containerd.CheckpointTaskOpts) (containerd.Image, error) {
	return nil, nil
}
func (t *fakeTask) Update(context.Context, ...containerd.UpdateTaskOpts) error                   { return nil }
func (t *fakeTask) LoadProcess(context.Context, string, containerdcio.Attach) (containerd.Process, error) {
	return nil, nil
}
func (t *fakeTask) Metrics(context.Context) (*apitypes.Metric, error)                            { return nil, nil }
func (t *fakeTask) Spec(context.Context) (*oci.Spec, error)                                      { return nil, nil }

func (t *fakeTask) Delete(ctx context.Context, opts ...containerd.ProcessDeleteOpts) (*containerd.ExitStatus, error) {
	for _, o := range opts {
		if err := o(ctx, t); err != nil {
			return nil, err
		}
	}
	if t.deleteErr != nil {
		return nil, t.deleteErr
	}
	es := containerd.NewExitStatus(0, time.Now(), nil)
	return es, nil
}

// fakeContainer implements containerd.Container for testing.
// Task() returns the configured fakeTask; other methods are no-ops.
type fakeContainer struct {
	id   string
	task containerd.Task
}

func (c *fakeContainer) ID() string { return c.id }
func (c *fakeContainer) Info(context.Context, ...containerd.InfoOpts) (containers.Container, error) {
	return containers.Container{ID: c.id}, nil
}
func (c *fakeContainer) Delete(context.Context, ...containerd.DeleteOpts) error  { return nil }
func (c *fakeContainer) NewTask(context.Context, containerdcio.Creator, ...containerd.NewTaskOpts) (containerd.Task, error) {
	return nil, nil
}
func (c *fakeContainer) Spec(context.Context) (*oci.Spec, error)                { return nil, nil }
func (c *fakeContainer) Task(_ context.Context, _ containerdcio.Attach) (containerd.Task, error) {
	if c.task == nil {
		return nil, errdefs.ErrNotFound
	}
	return c.task, nil
}
func (c *fakeContainer) Image(context.Context) (containerd.Image, error)                       { return nil, nil }
func (c *fakeContainer) Labels(context.Context) (map[string]string, error)                     { return nil, nil }
func (c *fakeContainer) SetLabels(context.Context, map[string]string) (map[string]string, error) {
	return nil, nil
}
func (c *fakeContainer) Extensions(context.Context) (map[string]typeurl.Any, error)            { return nil, nil }
func (c *fakeContainer) Update(context.Context, ...containerd.UpdateContainerOpts) error       { return nil }
func (c *fakeContainer) Checkpoint(context.Context, string, ...containerd.CheckpointOpts) (containerd.Image, error) {
	return nil, nil
}
func (c *fakeContainer) Restore(context.Context, containerdcio.Creator, string) (int, error)   { return 0, nil }

// fakeEventsTasksClient extends fakeTasksClient with a configurable Delete response.
type fakeEventsTasksClient struct {
	fakeTasksClient
	deleteErr error
}

func (f *fakeEventsTasksClient) Delete(_ context.Context, _ *tasks.DeleteTaskRequest, _ ...grpc.CallOption) (*tasks.DeleteResponse, error) {
	if f.deleteErr != nil {
		return nil, f.deleteErr
	}
	return &tasks.DeleteResponse{}, nil
}

// TestHandleContainerExit_RuntimeStateMissing verifies that handleContainerExit
// recovers gracefully when task.Delete returns "No such file or directory".
//
// This reproduces the stuck terminating pod bug: during node drain under IO
// pressure, a task delete can time out (handleEventTimeout=10s) while the
// delete still completes server-side, removing the OCI runtime state files.
// On retry, the kill step fails with "No such file or directory" because the
// state files are gone. Before the fix, this unrecognized error caused the
// event to be requeued indefinitely, leaving the pod stuck in Terminating.
func TestHandleContainerExit_RuntimeStateMissing(t *testing.T) {
	tests := []struct {
		name        string
		deleteErr   error
		expectError bool
		desc        string
	}{
		{
			name:        "errdefs ErrNotFound is handled (pre-existing behavior from PR #8954)",
			deleteErr:   errdefs.ErrNotFound,
			expectError: false,
			desc:        "containerd returns ErrNotFound when its own metadata has no record of the task",
		},
		{
			name:        "runtime state file missing is now handled (the fix)",
			deleteErr:   fmt.Errorf("rpc error: code = Unknown desc = failed to kill task 8882359abcdef: No such file or directory"),
			expectError: false,
			desc:        "crun/runc returns this when state files at /run/crun/<id>/status are already gone",
		},
		{
			name:        "other errors still propagate",
			deleteErr:   fmt.Errorf("connection refused"),
			expectError: true,
		},
		{
			name:        "no error on successful delete",
			deleteErr:   nil,
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			containerID := "test-container-id"

			mockTask := &fakeTask{id: containerID, deleteErr: tt.deleteErr}
			mockContainer := &fakeContainer{id: containerID, task: mockTask}

			taskSvc := &fakeEventsTasksClient{}
			c := newTestCRIServiceWithClient(taskSvc)
			c.nri = &nri.API{}
			c.containerEventsQ = eventq.New[*runtime.ContainerEventResponse](time.Minute, func(_ *runtime.ContainerEventResponse) {})

			cntr, err := containerstore.NewContainer(
				containerstore.Metadata{ID: containerID},
				containerstore.WithFakeStatus(containerstore.Status{
					CreatedAt: time.Now().UnixNano(),
					StartedAt: time.Now().UnixNano(),
				}),
				containerstore.WithContainer(mockContainer),
			)
			require.NoError(t, err)
			require.NoError(t, c.containerStore.Add(cntr))

			exitEvent := &eventtypes.TaskExit{
				ContainerID: containerID,
				Pid:         1,
				ExitStatus:  0,
				ExitedAt:    timestamppb.Now(),
			}

			err = c.handleContainerExit(context.Background(), exitEvent, cntr, "test-sandbox-id")
			if tt.expectError {
				assert.Error(t, err, "expected handleContainerExit to propagate the error")
				assert.Contains(t, err.Error(), "failed to stop container")
			} else {
				assert.NoError(t, err, "expected handleContainerExit to handle the error gracefully")
			}
		})
	}
}
