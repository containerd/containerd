// +build linux

package linux

import (
	"context"

	"google.golang.org/grpc"

	"github.com/containerd/containerd/api/services/shim"
	"github.com/containerd/containerd/api/types/task"
	"github.com/containerd/containerd/plugin"
	protobuf "github.com/gogo/protobuf/types"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
)

type Task struct {
	containerID string
	spec        []byte
	shim        shim.ShimClient
	namespace   string
}

func newTask(id, namespace string, spec []byte, shim shim.ShimClient) *Task {
	return &Task{
		containerID: id,
		shim:        shim,
		spec:        spec,
		namespace:   namespace,
	}
}

func (c *Task) Info() plugin.TaskInfo {
	return plugin.TaskInfo{
		ID:          c.containerID,
		ContainerID: c.containerID,
		Runtime:     pluginID,
		Spec:        c.spec,
		Namespace:   c.namespace,
	}
}

func (c *Task) Start(ctx context.Context) error {
	_, err := c.shim.Start(ctx, &shim.StartRequest{})
	if err != nil {
		err = errors.New(grpc.ErrorDesc(err))
	}
	return err
}

func (c *Task) State(ctx context.Context) (plugin.State, error) {
	response, err := c.shim.State(ctx, &shim.StateRequest{})
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

func (c *Task) Pause(ctx context.Context) error {
	_, err := c.shim.Pause(ctx, &shim.PauseRequest{})
	if err != nil {
		err = errors.New(grpc.ErrorDesc(err))
	}
	return err
}

func (c *Task) Resume(ctx context.Context) error {
	_, err := c.shim.Resume(ctx, &shim.ResumeRequest{})
	if err != nil {
		err = errors.New(grpc.ErrorDesc(err))
	}
	return err
}

func (c *Task) Kill(ctx context.Context, signal uint32, pid uint32, all bool) error {
	_, err := c.shim.Kill(ctx, &shim.KillRequest{
		Signal: signal,
		Pid:    pid,
		All:    all,
	})
	if err != nil {
		err = errors.New(grpc.ErrorDesc(err))
	}
	return err
}

func (c *Task) Exec(ctx context.Context, opts plugin.ExecOpts) (plugin.Process, error) {
	request := &shim.ExecRequest{
		Stdin:    opts.IO.Stdin,
		Stdout:   opts.IO.Stdout,
		Stderr:   opts.IO.Stderr,
		Terminal: opts.IO.Terminal,
		Spec: &protobuf.Any{
			TypeUrl: specs.Version,
			Value:   opts.Spec,
		},
	}
	resp, err := c.shim.Exec(ctx, request)
	if err != nil {
		return nil, errors.New(grpc.ErrorDesc(err))

	}
	return &Process{
		pid: int(resp.Pid),
		c:   c,
	}, nil
}

func (c *Task) Processes(ctx context.Context) ([]uint32, error) {
	resp, err := c.shim.Processes(ctx, &shim.ProcessesRequest{
		ID: c.containerID,
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

func (c *Task) Pty(ctx context.Context, pid uint32, size plugin.ConsoleSize) error {
	_, err := c.shim.Pty(ctx, &shim.PtyRequest{
		Pid:    pid,
		Width:  size.Width,
		Height: size.Height,
	})
	if err != nil {
		err = errors.New(grpc.ErrorDesc(err))
	}
	return err
}

func (c *Task) CloseStdin(ctx context.Context, pid uint32) error {
	_, err := c.shim.CloseStdin(ctx, &shim.CloseStdinRequest{
		Pid: pid,
	})
	if err != nil {
		err = errors.New(grpc.ErrorDesc(err))
	}
	return err
}

func (c *Task) Checkpoint(ctx context.Context, opts plugin.CheckpointOpts) error {
	_, err := c.shim.Checkpoint(ctx, &shim.CheckpointRequest{
		Exit:             opts.Exit,
		AllowTcp:         opts.AllowTCP,
		AllowUnixSockets: opts.AllowUnixSockets,
		AllowTerminal:    opts.AllowTerminal,
		FileLocks:        opts.FileLocks,
		EmptyNamespaces:  opts.EmptyNamespaces,
		CheckpointPath:   opts.Path,
	})
	if err != nil {
		err = errors.New(grpc.ErrorDesc(err))
	}
	return err
}

func (c *Task) DeleteProcess(ctx context.Context, pid uint32) (*plugin.Exit, error) {
	r, err := c.shim.DeleteProcess(ctx, &shim.DeleteProcessRequest{
		Pid: pid,
	})
	if err != nil {
		return nil, errors.New(grpc.ErrorDesc(err))
	}
	return &plugin.Exit{
		Status:    r.ExitStatus,
		Timestamp: r.ExitedAt,
	}, nil
}

type Process struct {
	pid int
	c   *Task
}

func (p *Process) Kill(ctx context.Context, signal uint32, _ bool) error {
	_, err := p.c.shim.Kill(ctx, &shim.KillRequest{
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
	state, err := p.c.State(ctx)
	if err != nil {
		return state, err
	}
	state.Pid = uint32(p.pid)
	return state, nil
}
