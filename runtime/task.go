// +build linux

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

package runtime

import (
	"context"
	"sync"

	"github.com/containerd/cgroups"
	eventstypes "github.com/containerd/containerd/api/events"
	"github.com/containerd/containerd/api/types/task"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/events/exchange"
	"github.com/containerd/containerd/identifiers"
	gruntime "github.com/containerd/containerd/runtime/generic"
	"github.com/containerd/containerd/runtime/shim/client"
	shim "github.com/containerd/containerd/runtime/shim/v1"
	runc "github.com/containerd/go-runc"
	"github.com/containerd/ttrpc"
	"github.com/gogo/protobuf/types"
	"github.com/pkg/errors"
)

// Task on a linux based system
type Task struct {
	mu        sync.Mutex
	id        string
	pid       int
	shim      *client.Client
	namespace string
	cg        cgroups.Cgroup
	monitor   gruntime.TaskMonitor
	events    *exchange.Exchange
	runtime   *runc.Runc
}

func newTask(id, namespace string, pid int, shim *client.Client, monitor gruntime.TaskMonitor, events *exchange.Exchange, runtime *runc.Runc) (*Task, error) {
	var (
		err error
		cg  cgroups.Cgroup
	)
	if pid > 0 {
		cg, err = cgroups.Load(cgroups.V1, cgroups.PidPath(pid))
		if err != nil && err != cgroups.ErrCgroupDeleted {
			return nil, err
		}
	}
	return &Task{
		id:        id,
		pid:       pid,
		shim:      shim,
		namespace: namespace,
		cg:        cg,
		monitor:   monitor,
		events:    events,
		runtime:   runtime,
	}, nil
}

// ID of the task
func (t *Task) ID() string {
	return t.id
}

// Info returns task information about the runtime and namespace
func (t *Task) Info() gruntime.TaskInfo {
	return gruntime.TaskInfo{
		ID:        t.id,
		Runtime:   pluginID,
		Namespace: t.namespace,
	}
}

// Start the task
func (t *Task) Start(ctx context.Context) error {
	t.mu.Lock()
	hasCgroup := t.cg != nil
	t.mu.Unlock()
	r, err := t.shim.Start(ctx, &shim.StartRequest{
		ID: t.id,
	})
	if err != nil {
		return errdefs.FromGRPC(err)
	}
	t.pid = int(r.Pid)
	if !hasCgroup {
		cg, err := cgroups.Load(cgroups.V1, cgroups.PidPath(t.pid))
		if err != nil {
			return err
		}
		t.mu.Lock()
		t.cg = cg
		t.mu.Unlock()
		if err := t.monitor.Monitor(t); err != nil {
			return err
		}
	}
	t.events.Publish(ctx, gruntime.TaskStartEventTopic, &eventstypes.TaskStart{
		ContainerID: t.id,
		Pid:         uint32(t.pid),
	})
	return nil
}

// State returns runtime information for the task
func (t *Task) State(ctx context.Context) (gruntime.State, error) {
	response, err := t.shim.State(ctx, &shim.StateRequest{
		ID: t.id,
	})
	if err != nil {
		if errors.Cause(err) != ttrpc.ErrClosed {
			return gruntime.State{}, errdefs.FromGRPC(err)
		}
		return gruntime.State{}, errdefs.ErrNotFound
	}
	var status gruntime.Status
	switch response.Status {
	case task.StatusCreated:
		status = gruntime.CreatedStatus
	case task.StatusRunning:
		status = gruntime.RunningStatus
	case task.StatusStopped:
		status = gruntime.StoppedStatus
	case task.StatusPaused:
		status = gruntime.PausedStatus
	case task.StatusPausing:
		status = gruntime.PausingStatus
	}
	return gruntime.State{
		Pid:        response.Pid,
		Status:     status,
		Stdin:      response.Stdin,
		Stdout:     response.Stdout,
		Stderr:     response.Stderr,
		Terminal:   response.Terminal,
		ExitStatus: response.ExitStatus,
		ExitedAt:   response.ExitedAt,
	}, nil
}

// Pause the task and all processes
func (t *Task) Pause(ctx context.Context) error {
	if _, err := t.shim.Pause(ctx, empty); err != nil {
		return errdefs.FromGRPC(err)
	}
	t.events.Publish(ctx, gruntime.TaskPausedEventTopic, &eventstypes.TaskPaused{
		ContainerID: t.id,
	})
	return nil
}

// Resume the task and all processes
func (t *Task) Resume(ctx context.Context) error {
	if _, err := t.shim.Resume(ctx, empty); err != nil {
		return errdefs.FromGRPC(err)
	}
	t.events.Publish(ctx, gruntime.TaskResumedEventTopic, &eventstypes.TaskResumed{
		ContainerID: t.id,
	})
	return nil
}

// Kill the task using the provided signal
//
// Optionally send the signal to all processes that are a child of the task
func (t *Task) Kill(ctx context.Context, signal uint32, all bool) error {
	if _, err := t.shim.Kill(ctx, &shim.KillRequest{
		ID:     t.id,
		Signal: signal,
		All:    all,
	}); err != nil {
		return errdefs.FromGRPC(err)
	}
	return nil
}

// Exec creates a new process inside the task
func (t *Task) Exec(ctx context.Context, id string, opts gruntime.ExecOpts) (gruntime.Process, error) {
	if err := identifiers.Validate(id); err != nil {
		return nil, errors.Wrapf(err, "invalid exec id")
	}
	request := &shim.ExecProcessRequest{
		ID:       id,
		Stdin:    opts.IO.Stdin,
		Stdout:   opts.IO.Stdout,
		Stderr:   opts.IO.Stderr,
		Terminal: opts.IO.Terminal,
		Spec:     opts.Spec,
	}
	if _, err := t.shim.Exec(ctx, request); err != nil {
		return nil, errdefs.FromGRPC(err)
	}
	return &Process{
		id: id,
		t:  t,
	}, nil
}

// Pids returns all system level process ids running inside the task
func (t *Task) Pids(ctx context.Context) ([]gruntime.ProcessInfo, error) {
	resp, err := t.shim.ListPids(ctx, &shim.ListPidsRequest{
		ID: t.id,
	})
	if err != nil {
		return nil, errdefs.FromGRPC(err)
	}
	var processList []gruntime.ProcessInfo
	for _, p := range resp.Processes {
		processList = append(processList, gruntime.ProcessInfo{
			Pid:  p.Pid,
			Info: p.Info,
		})
	}
	return processList, nil
}

// ResizePty changes the side of the task's PTY to the provided width and height
func (t *Task) ResizePty(ctx context.Context, size gruntime.ConsoleSize) error {
	_, err := t.shim.ResizePty(ctx, &shim.ResizePtyRequest{
		ID:     t.id,
		Width:  size.Width,
		Height: size.Height,
	})
	if err != nil {
		err = errdefs.FromGRPC(err)
	}
	return err
}

// CloseIO closes the provided IO on the task
func (t *Task) CloseIO(ctx context.Context) error {
	_, err := t.shim.CloseIO(ctx, &shim.CloseIORequest{
		ID:    t.id,
		Stdin: true,
	})
	if err != nil {
		err = errdefs.FromGRPC(err)
	}
	return err
}

// Checkpoint creates a system level dump of the task and process information that can be later restored
func (t *Task) Checkpoint(ctx context.Context, path string, options *types.Any) error {
	r := &shim.CheckpointTaskRequest{
		Path:    path,
		Options: options,
	}
	if _, err := t.shim.Checkpoint(ctx, r); err != nil {
		return errdefs.FromGRPC(err)
	}
	t.events.Publish(ctx, gruntime.TaskCheckpointedEventTopic, &eventstypes.TaskCheckpointed{
		ContainerID: t.id,
	})
	return nil
}

// DeleteProcess removes the provided process from the task and deletes all on disk state
func (t *Task) DeleteProcess(ctx context.Context, id string) (*gruntime.Exit, error) {
	r, err := t.shim.DeleteProcess(ctx, &shim.DeleteProcessRequest{
		ID: id,
	})
	if err != nil {
		return nil, errdefs.FromGRPC(err)
	}
	return &gruntime.Exit{
		Status:    r.ExitStatus,
		Timestamp: r.ExitedAt,
		Pid:       r.Pid,
	}, nil
}

// Update changes runtime information of a running task
func (t *Task) Update(ctx context.Context, resources *types.Any) error {
	if _, err := t.shim.Update(ctx, &shim.UpdateTaskRequest{
		Resources: resources,
	}); err != nil {
		return errdefs.FromGRPC(err)
	}
	return nil
}

// Process returns a specific process inside the task by the process id
func (t *Task) Process(ctx context.Context, id string) (gruntime.Process, error) {
	p := &Process{
		id: id,
		t:  t,
	}
	if _, err := p.State(ctx); err != nil {
		return nil, err
	}
	return p, nil
}

// Metrics returns runtime specific system level metric information for the task
func (t *Task) Metrics(ctx context.Context) (interface{}, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cg == nil {
		return nil, errors.Wrap(errdefs.ErrNotFound, "cgroup does not exist")
	}
	stats, err := t.cg.Stat(cgroups.IgnoreNotExist)
	if err != nil {
		return nil, err
	}
	return stats, nil
}

// Cgroup returns the underlying cgroup for a linux task
func (t *Task) Cgroup() (cgroups.Cgroup, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cg == nil {
		return nil, errors.Wrap(errdefs.ErrNotFound, "cgroup does not exist")
	}
	return t.cg, nil
}

// Wait for the task to exit returning the status and timestamp
func (t *Task) Wait(ctx context.Context) (*gruntime.Exit, error) {
	r, err := t.shim.Wait(ctx, &shim.WaitRequest{
		ID: t.id,
	})
	if err != nil {
		return nil, err
	}
	return &gruntime.Exit{
		Timestamp: r.ExitedAt,
		Status:    r.ExitStatus,
	}, nil
}
