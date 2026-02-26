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
	"syscall"
	"testing"
	"time"

	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/assert"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/containerd/containerd/api/types"
	"github.com/containerd/containerd/v2/client"
	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/containers"
	containerstore "github.com/containerd/containerd/v2/internal/cri/store/container"
	"github.com/containerd/containerd/v2/pkg/cio"
	"github.com/containerd/containerd/v2/pkg/oci"
	"github.com/containerd/errdefs"
	"github.com/containerd/typeurl/v2"
)

type fakeContainer struct {
	c containers.Container
	t fakeTask
}

// Checkpoint implements client.Container.
func (f *fakeContainer) Checkpoint(context.Context, string, ...client.CheckpointOpts) (client.Image, error) {
	panic("unimplemented")
}

// Delete implements client.Container.
func (f *fakeContainer) Delete(context.Context, ...client.DeleteOpts) error {
	return nil
}

// Extensions implements client.Container.
func (f *fakeContainer) Extensions(context.Context) (map[string]typeurl.Any, error) {
	panic("unimplemented")
}

// ID implements client.Container.
func (f *fakeContainer) ID() string {
	panic("unimplemented")
}

// Image implements client.Container.
func (f *fakeContainer) Image(context.Context) (client.Image, error) {
	panic("unimplemented")
}

// Info implements client.Container.
func (f *fakeContainer) Info(context.Context, ...client.InfoOpts) (containers.Container, error) {
	return f.c, nil
}

// Labels implements client.Container.
func (f *fakeContainer) Labels(context.Context) (map[string]string, error) {
	panic("unimplemented")
}

// NewTask implements client.Container.
func (f *fakeContainer) NewTask(context.Context, cio.Creator, ...client.NewTaskOpts) (client.Task, error) {
	panic("unimplemented")
}

// SetLabels implements client.Container.
func (f *fakeContainer) SetLabels(context.Context, map[string]string) (map[string]string, error) {
	panic("unimplemented")
}

// Spec implements client.Container.
func (f *fakeContainer) Spec(context.Context) (*specs.Spec, error) {
	return nil, errdefs.ErrNotImplemented
}

// Task implements client.Container.
func (f *fakeContainer) Task(context.Context, cio.Attach) (client.Task, error) {
	return &f.t, nil
}

// Update implements client.Container.
func (f *fakeContainer) Update(context.Context, ...client.UpdateContainerOpts) error {
	panic("unimplemented")
}

type fakeTask struct {
	id          string
	pid         uint32
	status      containerd.Status
	statusErr   error
	waitErr     error
	deleteErr   error
	waitExitCh  chan struct{}
	StatusMutex sync.Mutex
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
	defer f.StatusMutex.Unlock()
	f.StatusMutex.Lock()
	if f.deleteErr != nil {
		return nil, f.deleteErr
	}
	return containerd.NewExitStatus(f.status.ExitStatus, f.status.ExitTime, nil), nil
}

func (f *fakeTask) Kill(ctx context.Context, signal syscall.Signal, opts ...containerd.KillOpts) error {
	defer f.StatusMutex.Unlock()
	f.StatusMutex.Lock()
	f.status = containerd.Status{
		Status: containerd.Stopped,
	}
	// If the kill task is too fast and the wait task has not sent an exit signal to the channel,
	// the container status may not be set to Exited
	time.Sleep(time.Second * 1)
	return nil
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
	} else {
		ch <- *containerd.NewExitStatus(f.status.ExitStatus, f.status.ExitTime, nil)
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

// If the task is abnormal, the task will still be queried
// even when the container is in an exit state.
// Add testing for this abnormal state
func TestRemoveAbnormal(t *testing.T) {
	criservice := newTestCRIService()
	containers := []*struct {
		container fakeContainer
	}{
		{
			container: fakeContainer{
				t: fakeTask{
					id: "456",
					status: containerd.Status{
						Status: containerd.Created,
					},
					statusErr: nil,
					waitErr:   nil,
				},
				c: containers.Container{
					ID: "test",
				},
			},
		},
		{
			container: fakeContainer{
				t: fakeTask{
					id: "456",
					status: containerd.Status{
						Status: containerd.Running,
					},
					statusErr: nil,
					waitErr:   nil,
				},
				c: containers.Container{
					ID: "test",
				},
			},
		},
		{
			container: fakeContainer{
				t: fakeTask{
					id: "456",
					status: containerd.Status{
						Status: containerd.Stopped,
					},
					statusErr: nil,
					waitErr:   nil,
				},
				c: containers.Container{
					ID: "test",
				},
			},
		},
		{
			container: fakeContainer{
				t: fakeTask{
					id: "456",
					status: containerd.Status{
						Status: containerd.Paused,
					},
					statusErr: nil,
					waitErr:   nil,
				},
				c: containers.Container{
					ID: "test",
				},
			},
		},
		{
			container: fakeContainer{
				t: fakeTask{
					id: "456",
					status: containerd.Status{
						Status: containerd.Pausing,
					},
					statusErr: nil,
					waitErr:   nil,
				},
				c: containers.Container{
					ID: "test",
				},
			},
		},
		{
			container: fakeContainer{
				t: fakeTask{
					id: "456",
					status: containerd.Status{
						Status: containerd.Unknown,
					},
					statusErr: nil,
					waitErr:   nil,
				},
				c: containers.Container{
					ID: "test",
				},
			},
		},
	}

	for _, c := range containers {
		container, err := containerstore.NewContainer(
			containerstore.Metadata{ID: "123"},
			containerstore.WithFakeStatus(
				containerstore.Status{
					CreatedAt:  time.Now().UnixNano(),
					StartedAt:  time.Now().UnixNano(),
					FinishedAt: time.Now().UnixNano(),
				},
			),
			containerstore.WithContainer(&c.container),
		)
		assert.NoError(t, err)
		assert.Nil(t, container.Stats)
		criservice.containerStore.Add(container)
		cContainer, err := criservice.containerStore.Get("123")
		assert.NoError(t, err)
		_, err = cContainer.Container.Task(context.Background(), nil)
		assert.NoError(t, err)
		_, err = criservice.RemoveContainer(context.Background(), &runtime.RemoveContainerRequest{ContainerId: "123"})
		// Need to judge the error here
		assert.NoError(t, err)
	}

}

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
