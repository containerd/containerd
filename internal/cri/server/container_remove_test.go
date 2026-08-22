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
	"syscall"

	"github.com/containerd/typeurl/v2"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/containerd/containerd/api/types"
	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/containers"
	"github.com/containerd/containerd/v2/internal/eventq"
	"github.com/containerd/containerd/v2/pkg/cio"
	"github.com/containerd/errdefs"
	"github.com/opencontainers/runtime-spec/specs-go"

	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	containerstore "github.com/containerd/containerd/v2/internal/cri/store/container"
)

// TestSetContainerRemoving tests setContainerRemoving sets removing
// state correctly.
func TestSetContainerRemoving(t *testing.T) {
	testID := "test-id"
	for _, test := range []struct {
		desc      string
		status    containerstore.Status
		expectErr bool
	}{
		{
			desc: "should return error when container is in running state",
			status: containerstore.Status{
				CreatedAt: time.Now().UnixNano(),
				StartedAt: time.Now().UnixNano(),
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
				CreatedAt:  time.Now().UnixNano(),
				StartedAt:  time.Now().UnixNano(),
				FinishedAt: time.Now().UnixNano(),
				Removing:   true,
			},
			expectErr: true,
		},
		{
			desc: "should not return error when container is not running and removing",
			status: containerstore.Status{
				CreatedAt:  time.Now().UnixNano(),
				StartedAt:  time.Now().UnixNano(),
				FinishedAt: time.Now().UnixNano(),
			},
			expectErr: false,
		},
	} {
		t.Run(test.desc, func(t *testing.T) {
			container, err := containerstore.NewContainer(
				containerstore.Metadata{ID: testID},
				containerstore.WithFakeStatus(test.status),
			)
			assert.NoError(t, err)
			err = setContainerRemoving(container)
			if test.expectErr {
				assert.Error(t, err)
				assert.Equal(t, test.status, container.Status.Get(), "metadata should not be updated")
			} else {
				assert.NoError(t, err)
				assert.True(t, container.Status.Get().Removing, "removing should be set")
				assert.NoError(t, resetContainerRemoving(container))
				assert.False(t, container.Status.Get().Removing, "removing should be reset")
			}
		})
	}
}

func TestRemoveContainerOrphanTaskRetry(t *testing.T) {
	for _, test := range []struct {
		name           string
		task           *fakeTask
		taskErr        error
		wantTaskDelete bool
		wantErr        string
	}{
		{
			name:           "task not found proceeds to delete metadata",
			task:           nil,
			taskErr:        errdefs.ErrNotFound,
			wantTaskDelete: false,
		},
		{
			name:           "task exists and delete succeeds",
			task:           &fakeTask{},
			taskErr:        nil,
			wantTaskDelete: true,
		},
		{
			name:           "task exists but delete fails",
			task:           &fakeTask{deleteErr: errors.New("ebusy")},
			taskErr:        nil,
			wantTaskDelete: true,
			wantErr:        "failed to delete orphaned task for container",
		},
		{
			name:           "task retrieval fails with unknown error",
			task:           nil,
			taskErr:        errors.New("unknown error"),
			wantTaskDelete: false,
			wantErr:        "failed to get task for container",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := newTestCRIService()
			c.containerEventsQ = eventq.New[*runtime.ContainerEventResponse](time.Minute, func(*runtime.ContainerEventResponse) {})
			fake := &fakeRemoveContainerdContainer{
				id:      "test-id",
				task:    test.task,
				taskErr: test.taskErr,
			}

			cntr, err := containerstore.NewContainer(
				containerstore.Metadata{ID: "test-id", SandboxID: "test-sandbox-id"},
				containerstore.WithContainer(fake),
				containerstore.WithFakeStatus(containerstore.Status{
					CreatedAt:  time.Now().UnixNano(),
					StartedAt:  time.Now().UnixNano(),
					FinishedAt: time.Now().UnixNano(),
				}),
			)
			require.NoError(t, err)
			require.NoError(t, c.containerStore.Add(cntr))

			_, err = c.RemoveContainer(context.Background(), &runtime.RemoveContainerRequest{ContainerId: "test-id"})
			if test.wantErr != "" {
				require.ErrorContains(t, err, test.wantErr)
			} else {
				require.NoError(t, err)
				require.True(t, fake.deleted)
				if test.task != nil {
					require.True(t, test.task.deleted)
				}
			}
		})
	}
}

type fakeTask struct {
	deleted   bool
	deleteErr error
}

func (f *fakeTask) ID() string                  { return "test-id" }
func (f *fakeTask) Pid() uint32                 { return 1234 }
func (f *fakeTask) Start(context.Context) error { return errdefs.ErrNotImplemented }
func (f *fakeTask) Delete(context.Context, ...containerd.ProcessDeleteOpts) (*containerd.ExitStatus, error) {
	f.deleted = true
	return nil, f.deleteErr
}
func (f *fakeTask) Kill(context.Context, syscall.Signal, ...containerd.KillOpts) error {
	return errdefs.ErrNotImplemented
}
func (f *fakeTask) Wait(context.Context) (<-chan containerd.ExitStatus, error) {
	return nil, errdefs.ErrNotImplemented
}
func (f *fakeTask) CloseIO(context.Context, ...containerd.IOCloserOpts) error {
	return errdefs.ErrNotImplemented
}
func (f *fakeTask) Resize(context.Context, uint32, uint32) error { return errdefs.ErrNotImplemented }
func (f *fakeTask) IO() cio.IO                                   { return nil }
func (f *fakeTask) Status(context.Context) (containerd.Status, error) {
	return containerd.Status{}, errdefs.ErrNotImplemented
}
func (f *fakeTask) Pause(context.Context) error  { return errdefs.ErrNotImplemented }
func (f *fakeTask) Resume(context.Context) error { return errdefs.ErrNotImplemented }
func (f *fakeTask) Exec(context.Context, string, *specs.Process, cio.Creator) (containerd.Process, error) {
	return nil, errdefs.ErrNotImplemented
}
func (f *fakeTask) Pids(context.Context) ([]containerd.ProcessInfo, error) {
	return nil, errdefs.ErrNotImplemented
}
func (f *fakeTask) Checkpoint(context.Context, ...containerd.CheckpointTaskOpts) (containerd.Image, error) {
	return nil, errdefs.ErrNotImplemented
}
func (f *fakeTask) Update(context.Context, ...containerd.UpdateTaskOpts) error {
	return errdefs.ErrNotImplemented
}
func (f *fakeTask) LoadProcess(context.Context, string, cio.Attach) (containerd.Process, error) {
	return nil, errdefs.ErrNotImplemented
}
func (f *fakeTask) Metrics(context.Context) (*types.Metric, error) {
	return nil, errdefs.ErrNotImplemented
}
func (f *fakeTask) Spec(context.Context) (*specs.Spec, error) { return nil, errdefs.ErrNotImplemented }

type fakeRemoveContainerdContainer struct {
	id      string
	task    *fakeTask
	taskErr error
	deleted bool
}

func (f *fakeRemoveContainerdContainer) ID() string { return f.id }
func (f *fakeRemoveContainerdContainer) Info(context.Context, ...containerd.InfoOpts) (containers.Container, error) {
	return containers.Container{}, nil
}
func (f *fakeRemoveContainerdContainer) Delete(context.Context, ...containerd.DeleteOpts) error {
	f.deleted = true
	return nil
}
func (f *fakeRemoveContainerdContainer) NewTask(context.Context, cio.Creator, ...containerd.NewTaskOpts) (containerd.Task, error) {
	return nil, errdefs.ErrNotImplemented
}
func (f *fakeRemoveContainerdContainer) Spec(context.Context) (*specs.Spec, error) {
	return nil, errdefs.ErrNotImplemented
}
func (f *fakeRemoveContainerdContainer) Task(context.Context, cio.Attach) (containerd.Task, error) {
	if f.taskErr != nil {
		return nil, f.taskErr
	}
	return f.task, nil
}
func (f *fakeRemoveContainerdContainer) Image(context.Context) (containerd.Image, error) {
	return nil, errdefs.ErrNotImplemented
}
func (f *fakeRemoveContainerdContainer) Labels(context.Context) (map[string]string, error) {
	return nil, errdefs.ErrNotImplemented
}
func (f *fakeRemoveContainerdContainer) SetLabels(context.Context, map[string]string) (map[string]string, error) {
	return nil, errdefs.ErrNotImplemented
}
func (f *fakeRemoveContainerdContainer) Extensions(context.Context) (map[string]typeurl.Any, error) {
	return nil, errdefs.ErrNotImplemented
}
func (f *fakeRemoveContainerdContainer) Update(context.Context, ...containerd.UpdateContainerOpts) error {
	return errdefs.ErrNotImplemented
}
func (f *fakeRemoveContainerdContainer) Checkpoint(context.Context, string, ...containerd.CheckpointOpts) (containerd.Image, error) {
	return nil, errdefs.ErrNotImplemented
}
func (f *fakeRemoveContainerdContainer) Restore(context.Context, cio.Creator, string) (int, error) {
	return 0, errdefs.ErrNotImplemented
}
