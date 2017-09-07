// +build linux

package linux

import (
	"context"

	"google.golang.org/grpc"

	"github.com/containerd/cgroups"
	"github.com/containerd/containerd/api/types/task"
	"github.com/containerd/containerd/errdefs"
	client "github.com/containerd/containerd/linux/shim"
	shim "github.com/containerd/containerd/linux/shim/v1"
	"github.com/containerd/containerd/runtime"
	"github.com/gogo/protobuf/types"
)

type Task struct {
	id        string
	pid       int
	shim      *client.Client
	namespace string
	cg        cgroups.Cgroup
}

func newTask(id, namespace string, pid int, shim *client.Client) (*Task, error) {
	cg, err := cgroups.Load(cgroups.V1, cgroups.PidPath(pid))
	if err != nil {
		return nil, err
	}
	return &Task{
		id:        id,
		pid:       pid,
		shim:      shim,
		namespace: namespace,
		cg:        cg,
	}, nil
}

func (t *Task) ID() string {
	return t.id
}

func (t *Task) Info() runtime.TaskInfo {
	return runtime.TaskInfo{
		ID:        t.id,
		Runtime:   pluginID,
		Namespace: t.namespace,
	}
}

func (t *Task) Start(ctx context.Context) error {
	_, err := t.shim.Start(ctx, &shim.StartRequest{
		ID: t.id,
	})
	if err != nil {
		return errdefs.FromGRPC(err)
	}
	return nil
}

func (t *Task) State(ctx context.Context) (runtime.State, error) {
	response, err := t.shim.State(ctx, &shim.StateRequest{
		ID: t.id,
	})
	if err != nil {
		if err != grpc.ErrServerStopped {
			return runtime.State{}, errdefs.FromGRPC(err)
		}
		return runtime.State{}, errdefs.ErrNotFound
	}
	var status runtime.Status
	switch response.Status {
	case task.StatusCreated:
		status = runtime.CreatedStatus
	case task.StatusRunning:
		status = runtime.RunningStatus
	case task.StatusStopped:
		status = runtime.StoppedStatus
	case task.StatusPaused:
		status = runtime.PausedStatus
	case task.StatusPausing:
		status = runtime.PausingStatus
	}
	return runtime.State{
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

func (t *Task) Pause(ctx context.Context) error {
	_, err := t.shim.Pause(ctx, empty)
	if err != nil {
		err = errdefs.FromGRPC(err)
	}
	return err
}

func (t *Task) Resume(ctx context.Context) error {
	if _, err := t.shim.Resume(ctx, empty); err != nil {
		return errdefs.FromGRPC(err)
	}
	return nil
}

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

func (t *Task) Exec(ctx context.Context, id string, opts runtime.ExecOpts) (runtime.Process, error) {
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

func (t *Task) Pids(ctx context.Context) ([]uint32, error) {
	resp, err := t.shim.ListPids(ctx, &shim.ListPidsRequest{
		ID: t.id,
	})
	if err != nil {
		return nil, errdefs.FromGRPC(err)
	}
	return resp.Pids, nil
}

func (t *Task) ResizePty(ctx context.Context, size runtime.ConsoleSize) error {
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

func (t *Task) Checkpoint(ctx context.Context, path string, options *types.Any) error {
	r := &shim.CheckpointTaskRequest{
		Path:    path,
		Options: options,
	}
	if _, err := t.shim.Checkpoint(ctx, r); err != nil {
		return errdefs.FromGRPC(err)
	}
	return nil
}

func (t *Task) DeleteProcess(ctx context.Context, id string) (*runtime.Exit, error) {
	r, err := t.shim.DeleteProcess(ctx, &shim.DeleteProcessRequest{
		ID: id,
	})
	if err != nil {
		return nil, errdefs.FromGRPC(err)
	}
	return &runtime.Exit{
		Status:    r.ExitStatus,
		Timestamp: r.ExitedAt,
		Pid:       r.Pid,
	}, nil
}

func (t *Task) Update(ctx context.Context, resources *types.Any) error {
	if _, err := t.shim.Update(ctx, &shim.UpdateTaskRequest{
		Resources: resources,
	}); err != nil {
		return errdefs.FromGRPC(err)
	}
	return nil
}

func (t *Task) Process(ctx context.Context, id string) (runtime.Process, error) {
	// TODO: verify process exists for container
	return &Process{
		id: id,
		t:  t,
	}, nil
}

func (t *Task) Metrics(ctx context.Context) (interface{}, error) {
	stats, err := t.cg.Stat(cgroups.IgnoreNotExist)
	if err != nil {
		return nil, err
	}
	return stats, nil
}

func (t *Task) Cgroup() cgroups.Cgroup {
	return t.cg
}
