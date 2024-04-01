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

package podsandbox

import (
	"context"
	"errors"
	"syscall"
	"testing"
	"time"

	"github.com/containerd/errdefs"
	"github.com/containerd/typeurl/v2"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/assert"

	"github.com/containerd/containerd/v2/api/types"
	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/containers"
	criconfig "github.com/containerd/containerd/v2/internal/cri/config"
	crilabels "github.com/containerd/containerd/v2/internal/cri/labels"
	"github.com/containerd/containerd/v2/internal/cri/store/sandbox"
	"github.com/containerd/containerd/v2/pkg/cio"
	"github.com/containerd/containerd/v2/pkg/oci"
)

type fakeContainer struct {
	c       containers.Container
	t       fakeTask
	taskErr error
}

type fakeTask struct {
	id         string
	pid        uint32
	status     containerd.Status
	statusErr  error
	waitErr    error
	deleteErr  error
	waitExitCh chan struct{}
}

func (f *fakeTask) ID() string {
	return f.id
}

func (f *fakeTask) Pid() uint32 {
	return f.pid
}

func (f *fakeTask) Start(ctx context.Context) error {
	return nil
}

func (f *fakeTask) Delete(ctx context.Context, opts ...containerd.ProcessDeleteOpts) (*containerd.ExitStatus, error) {
	if f.deleteErr != nil {
		return nil, f.deleteErr
	}
	return containerd.NewExitStatus(f.status.ExitStatus, f.status.ExitTime, nil), nil
}

func (f *fakeTask) Kill(ctx context.Context, signal syscall.Signal, opts ...containerd.KillOpts) error {
	return errdefs.ErrNotImplemented
}

func (f *fakeTask) Wait(ctx context.Context) (<-chan containerd.ExitStatus, error) {
	if f.waitErr != nil {
		return nil, f.waitErr
	}
	ch := make(chan containerd.ExitStatus, 1)
	if f.waitExitCh != nil {
		go func() {
			<-f.waitExitCh
			ch <- *containerd.NewExitStatus(f.status.ExitStatus, f.status.ExitTime, nil)
		}()
	}

	return ch, nil
}

func (f *fakeTask) CloseIO(ctx context.Context, opts ...containerd.IOCloserOpts) error {
	return errdefs.ErrNotImplemented
}

func (f *fakeTask) Resize(ctx context.Context, w, h uint32) error {
	return errdefs.ErrNotImplemented
}

func (f *fakeTask) IO() cio.IO {
	return nil
}

func (f *fakeTask) Status(ctx context.Context) (containerd.Status, error) {
	if f.statusErr != nil {
		return containerd.Status{}, f.statusErr
	}
	return f.status, nil
}

func (f *fakeTask) Pause(ctx context.Context) error {
	return errdefs.ErrNotImplemented
}

func (f *fakeTask) Resume(ctx context.Context) error {
	return errdefs.ErrNotImplemented
}

func (f *fakeTask) Exec(ctx context.Context, s string, process *specs.Process, creator cio.Creator) (containerd.Process, error) {
	return nil, errdefs.ErrNotImplemented
}

func (f *fakeTask) Pids(ctx context.Context) ([]containerd.ProcessInfo, error) {
	return []containerd.ProcessInfo{}, errdefs.ErrNotImplemented
}

func (f *fakeTask) Checkpoint(ctx context.Context, opts ...containerd.CheckpointTaskOpts) (containerd.Image, error) {
	return nil, errdefs.ErrNotImplemented
}

func (f *fakeTask) Update(ctx context.Context, opts ...containerd.UpdateTaskOpts) error {
	return errdefs.ErrNotImplemented
}

func (f *fakeTask) LoadProcess(ctx context.Context, s string, attach cio.Attach) (containerd.Process, error) {
	return nil, errdefs.ErrNotImplemented
}

func (f *fakeTask) Metrics(ctx context.Context) (*types.Metric, error) {
	return nil, errdefs.ErrNotImplemented
}

func (f *fakeTask) Spec(ctx context.Context) (*oci.Spec, error) {
	return nil, errdefs.ErrNotImplemented
}

func (f *fakeContainer) ID() string {
	return f.c.ID
}

func (f *fakeContainer) Info(ctx context.Context, opts ...containerd.InfoOpts) (containers.Container, error) {
	return f.c, nil
}

func (f *fakeContainer) Delete(ctx context.Context, opts ...containerd.DeleteOpts) error {
	return nil
}

func (f *fakeContainer) NewTask(ctx context.Context, creator cio.Creator, opts ...containerd.NewTaskOpts) (containerd.Task, error) {
	return nil, errdefs.ErrNotImplemented
}

func (f *fakeContainer) Spec(ctx context.Context) (*oci.Spec, error) {
	return nil, errdefs.ErrNotImplemented
}

func (f *fakeContainer) Task(ctx context.Context, attach cio.Attach) (containerd.Task, error) {
	if f.taskErr != nil {
		return nil, f.taskErr
	}
	return &f.t, nil
}

func (f *fakeContainer) Image(ctx context.Context) (containerd.Image, error) {
	return nil, errdefs.ErrNotImplemented
}

func (f *fakeContainer) Labels(ctx context.Context) (map[string]string, error) {
	return f.c.Labels, nil
}

func (f *fakeContainer) SetLabels(ctx context.Context, m map[string]string) (map[string]string, error) {
	return nil, errdefs.ErrNotImplemented
}

func (f *fakeContainer) Extensions(ctx context.Context) (map[string]typeurl.Any, error) {
	return f.c.Extensions, nil
}

func (f *fakeContainer) Update(ctx context.Context, opts ...containerd.UpdateContainerOpts) error {
	return errdefs.ErrNotImplemented
}

func (f *fakeContainer) Checkpoint(ctx context.Context, s string, opts ...containerd.CheckpointOpts) (containerd.Image, error) {
	return nil, errdefs.ErrNotImplemented
}

func sandboxExtension(id string) map[string]typeurl.Any {
	metadata := sandbox.Metadata{
		ID: id,
	}

	ext, _ := typeurl.MarshalAny(&metadata)
	return map[string]typeurl.Any{
		crilabels.SandboxMetadataExtension: ext,
	}
}

func TestRecoverContainer(t *testing.T) {
	controller := &Controller{
		config: criconfig.Config{},
		store:  NewStore(),
	}
	containers := []struct {
		container        fakeContainer
		expectedState    sandbox.State
		expectedPid      uint32
		expectedExitCode uint32
	}{
		// sandbox container with task status running, and wait returns exit after 100 millisecond
		{
			container: fakeContainer{
				c: containers.Container{
					ID:         "sandbox_ready_container",
					CreatedAt:  time.Time{},
					UpdatedAt:  time.Time{},
					Extensions: sandboxExtension("sandbox_ready_container"),
				},
				t: fakeTask{
					id:  "sandbox_ready_task",
					pid: 233333,
					status: containerd.Status{
						Status:     containerd.Running,
						ExitStatus: 128,
						ExitTime:   time.Time{},
					},
					statusErr:  nil,
					waitErr:    nil,
					waitExitCh: make(chan struct{}),
				},
			},
			expectedState:    sandbox.StateReady,
			expectedPid:      233333,
			expectedExitCode: 128,
		},

		// sandbox container with task status return error
		{
			container: fakeContainer{
				c: containers.Container{
					ID:         "sandbox_task_error",
					CreatedAt:  time.Time{},
					UpdatedAt:  time.Time{},
					Extensions: sandboxExtension("sandbox_task_error"),
				},
				t: fakeTask{
					id:        "task_status_error",
					statusErr: errors.New("some unknown error"),
				},
			},
			expectedState: sandbox.StateUnknown,
		},

		// sandbox container with task status return not found
		{
			container: fakeContainer{
				c: containers.Container{
					ID:         "sandbox_task_status_not_found",
					CreatedAt:  time.Time{},
					UpdatedAt:  time.Time{},
					Extensions: sandboxExtension("sandbox_task_status_not_found"),
				},
				t: fakeTask{
					id:        "task_status_not_found",
					statusErr: errdefs.ErrNotFound,
				},
			},
			expectedState: sandbox.StateNotReady,
		},

		// sandbox container with task not found
		{
			container: fakeContainer{
				c: containers.Container{
					ID:         "sandbox_task_not_found",
					CreatedAt:  time.Time{},
					UpdatedAt:  time.Time{},
					Extensions: sandboxExtension("sandbox_task_not_found"),
				},
				taskErr: errdefs.ErrNotFound,
			},
			expectedState: sandbox.StateNotReady,
		},

		// sandbox container with error when call Task()
		{
			container: fakeContainer{
				c: containers.Container{
					ID:         "sandbox_task_error",
					CreatedAt:  time.Time{},
					UpdatedAt:  time.Time{},
					Extensions: sandboxExtension("sandbox_task_error"),
				},
				taskErr: errors.New("some unknown error"),
			},
			expectedState: sandbox.StateUnknown,
		},

		// sandbox container with task wait error
		{
			container: fakeContainer{
				c: containers.Container{
					ID:         "sandbox_task_wait_error",
					CreatedAt:  time.Time{},
					UpdatedAt:  time.Time{},
					Extensions: sandboxExtension("sandbox_task_wait_error"),
				},
				t: fakeTask{
					id:  "task_wait_error",
					pid: 10000,
					status: containerd.Status{
						Status: containerd.Running,
					},
					waitErr: errors.New("some unknown error"),
				},
			},
			expectedState: sandbox.StateUnknown,
		},

		// sandbox container with task wait not found
		{
			container: fakeContainer{
				c: containers.Container{
					ID:         "sandbox_task_wait_not_found",
					CreatedAt:  time.Time{},
					UpdatedAt:  time.Time{},
					Extensions: sandboxExtension("sandbox_task_wait_not_found"),
				},
				t: fakeTask{
					id:  "task_wait_not_found",
					pid: 10000,
					status: containerd.Status{
						Status: containerd.Running,
					},
					waitErr: errdefs.ErrNotFound,
				},
			},
			expectedState: sandbox.StateNotReady,
		},

		// sandbox container with task delete error
		{
			container: fakeContainer{
				c: containers.Container{
					ID:         "sandbox_task_delete_error",
					CreatedAt:  time.Time{},
					UpdatedAt:  time.Time{},
					Extensions: sandboxExtension("sandbox_task_delete_error"),
				},
				t: fakeTask{
					id: "task_delete_error",
					status: containerd.Status{
						Status:     containerd.Stopped,
						ExitStatus: 128,
						ExitTime:   time.Time{},
					},
					deleteErr: errors.New("some unknown error"),
				},
			},
			expectedState: sandbox.StateUnknown,
		},

		// sandbox container with task delete not found
		{
			container: fakeContainer{
				c: containers.Container{
					ID:         "sandbox_task_delete_not_found",
					CreatedAt:  time.Time{},
					UpdatedAt:  time.Time{},
					Extensions: sandboxExtension("sandbox_task_delete_not_found"),
				},
				t: fakeTask{
					id: "task_delete_not_found",
					status: containerd.Status{
						Status:     containerd.Created,
						ExitStatus: 128,
						ExitTime:   time.Time{},
					},
					deleteErr: errdefs.ErrNotFound,
				},
			},
			expectedState: sandbox.StateNotReady,
		},
	}

	for _, c := range containers {
		cont := c.container
		sb, err := controller.RecoverContainer(context.Background(), &cont)
		assert.NoError(t, err)

		pSb := controller.store.Get(cont.ID())
		assert.NotNil(t, pSb)
		assert.Equal(t, c.expectedState, pSb.Status.Get().State, "%s state is not expected", cont.ID())

		if c.expectedExitCode > 0 {
			cont.t.waitExitCh <- struct{}{}
			exitStatus, _ := pSb.Wait(context.Background())
			assert.Equal(t, c.expectedExitCode, exitStatus.ExitCode(), "%s state is not expected", cont.ID())
		}
		status := sb.Status.Get()
		assert.Equal(t, c.expectedState, status.State, "%s sandbox state is not expected", cont.ID())
		if c.expectedPid > 0 {
			assert.Equal(t, c.expectedPid, status.Pid, "%s sandbox pid is not expected", cont.ID())
		}
	}

}
