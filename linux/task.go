// +build linux

package linux

import (
	"context"

	"google.golang.org/grpc"

	"github.com/containerd/containerd/api/types/task"
	client "github.com/containerd/containerd/linux/shim"
	shim "github.com/containerd/containerd/linux/shim/v1"
	"github.com/containerd/containerd/plugin"
	protobuf "github.com/gogo/protobuf/types"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
)

type Task struct {
	containerID string
	spec        []byte
	shim        *client.Client
	namespace   string
}

func newTask(id, namespace string, spec []byte, shim *client.Client) *Task {
	return &Task{
		containerID: id,
		shim:        shim,
		spec:        spec,
		namespace:   namespace,
	}
}

func (t *Task) Info() plugin.TaskInfo {
	return plugin.TaskInfo{
		ID:          t.containerID,
		ContainerID: t.containerID,
		Runtime:     pluginID,
		Spec:        t.spec,
		Namespace:   t.namespace,
	}
}

func (t *Task) Start(ctx context.Context) error {
	_, err := t.shim.Start(ctx, empty)
	if err != nil {
		err = errors.New(grpc.ErrorDesc(err))
	}
	return err
}

func (t *Task) State(ctx context.Context) (plugin.State, error) {
	response, err := t.shim.State(ctx, empty)
	if err != nil {
		return plugin.State{}, errors.New(grpc.ErrorDesc(err))
	}
	var status plugin.Status
	switch response.Status {
	case task.StatusCreated:
		status = plugin.CreatedStatus
	case task.StatusRunning:
		status = plugin.RunningStatus
	case task.StatusStopped:
		status = plugin.StoppedStatus
	case task.StatusPaused:
		status = plugin.PausedStatus
		// TODO: containerd.DeletedStatus
	}
	return plugin.State{
		Pid:      response.Pid,
		Status:   status,
		Stdin:    response.Stdin,
		Stdout:   response.Stdout,
		Stderr:   response.Stderr,
		Terminal: response.Terminal,
	}, nil
}

func (t *Task) Pause(ctx context.Context) error {
	_, err := t.shim.Pause(ctx, empty)
	if err != nil {
		err = errors.New(grpc.ErrorDesc(err))
	}
	return err
}

func (t *Task) Resume(ctx context.Context) error {
	_, err := t.shim.Resume(ctx, empty)
	if err != nil {
		err = errors.New(grpc.ErrorDesc(err))
	}
	return err
}

func (t *Task) Kill(ctx context.Context, signal uint32, pid uint32, all bool) error {
	_, err := t.shim.Kill(ctx, &shim.KillRequest{
		Signal: signal,
		Pid:    pid,
		All:    all,
	})
	if err != nil {
		err = errors.New(grpc.ErrorDesc(err))
	}
	return err
}

func (t *Task) Exec(ctx context.Context, opts plugin.ExecOpts) (plugin.Process, error) {
	request := &shim.ExecProcessRequest{
		Stdin:    opts.IO.Stdin,
		Stdout:   opts.IO.Stdout,
		Stderr:   opts.IO.Stderr,
		Terminal: opts.IO.Terminal,
		Spec: &protobuf.Any{
			TypeUrl: specs.Version,
			Value:   opts.Spec,
		},
	}
	resp, err := t.shim.Exec(ctx, request)
	if err != nil {
		return nil, errors.New(grpc.ErrorDesc(err))

	}
	return &Process{
		pid: int(resp.Pid),
		t:   t,
	}, nil
}

func (t *Task) Processes(ctx context.Context) ([]uint32, error) {
	resp, err := t.shim.ListProcesses(ctx, &shim.ListProcessesRequest{
		ID: t.containerID,
	})
	if err != nil {
		return nil, errors.New(grpc.ErrorDesc(err))
	}

	pids := make([]uint32, 0, len(resp.Processes))

	for _, ps := range resp.Processes {
		pids = append(pids, ps.Pid)
	}

	return pids, nil
}

func (t *Task) ResizePty(ctx context.Context, pid uint32, size plugin.ConsoleSize) error {
	_, err := t.shim.ResizePty(ctx, &shim.ResizePtyRequest{
		Pid:    pid,
		Width:  size.Width,
		Height: size.Height,
	})
	if err != nil {
		err = errors.New(grpc.ErrorDesc(err))
	}
	return err
}

func (t *Task) CloseIO(ctx context.Context, pid uint32) error {
	_, err := t.shim.CloseIO(ctx, &shim.CloseIORequest{
		Pid:   pid,
		Stdin: true,
	})
	if err != nil {
		err = errors.New(grpc.ErrorDesc(err))
	}
	return err
}

func (t *Task) Checkpoint(ctx context.Context, path string, options map[string]string) error {
	r := &shim.CheckpointTaskRequest{
		Path:    path,
		Options: options,
	}
	if _, err := t.shim.Checkpoint(ctx, r); err != nil {
		return errors.New(grpc.ErrorDesc(err))
	}
	return nil
}

func (t *Task) DeleteProcess(ctx context.Context, pid uint32) (*plugin.Exit, error) {
	r, err := t.shim.DeleteProcess(ctx, &shim.DeleteProcessRequest{
		Pid: pid,
	})
	if err != nil {
		return nil, errors.New(grpc.ErrorDesc(err))
	}
	return &plugin.Exit{
		Status:    r.ExitStatus,
		Timestamp: r.ExitedAt,
		Pid:       pid,
	}, nil
}

type Process struct {
	pid int
	t   *Task
}

func (p *Process) Kill(ctx context.Context, signal uint32, _ bool) error {
	_, err := p.t.shim.Kill(ctx, &shim.KillRequest{
		Signal: signal,
		Pid:    uint32(p.pid),
	})
	if err != nil {
		err = errors.New(grpc.ErrorDesc(err))
	}
	return err
}

func (p *Process) State(ctx context.Context) (plugin.State, error) {
	// use the container status for the status of the process
	state, err := p.t.State(ctx)
	if err != nil {
		return state, err
	}
	state.Pid = uint32(p.pid)
	return state, nil
}
