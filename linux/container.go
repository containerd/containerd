// +build linux

package linux

import (
	"context"

	"github.com/containerd/containerd/api/services/shim"
	"github.com/containerd/containerd/api/types/container"
	"github.com/containerd/containerd/plugin"
	protobuf "github.com/gogo/protobuf/types"
	specs "github.com/opencontainers/runtime-spec/specs-go"
)

type State struct {
	pid    uint32
	status plugin.Status
}

func (s State) Pid() uint32 {
	return s.pid
}

func (s State) Status() plugin.Status {
	return s.status
}

func newContainer(id string, shim shim.ShimClient) *Container {
	return &Container{
		id:   id,
		shim: shim,
	}
}

type Container struct {
	id string

	shim shim.ShimClient
}

func (c *Container) Info() plugin.ContainerInfo {
	return plugin.ContainerInfo{
		ID:      c.id,
		Runtime: runtimeName,
	}
}

func (c *Container) Start(ctx context.Context) error {
	_, err := c.shim.Start(ctx, &shim.StartRequest{})
	return err
}

func (c *Container) State(ctx context.Context) (plugin.State, error) {
	response, err := c.shim.State(ctx, &shim.StateRequest{})
	if err != nil {
		return nil, err
	}
	var status plugin.Status
	switch response.Status {
	case container.StatusCreated:
		status = plugin.CreatedStatus
	case container.StatusRunning:
		status = plugin.RunningStatus
	case container.StatusStopped:
		status = plugin.StoppedStatus
	case container.StatusPaused:
		status = plugin.PausedStatus
		// TODO: containerd.DeletedStatus
	}
	return &State{
		pid:    response.Pid,
		status: status,
	}, nil
}

func (c *Container) Pause(ctx context.Context) error {
	_, err := c.shim.Pause(ctx, &shim.PauseRequest{})
	return err
}

func (c *Container) Resume(ctx context.Context) error {
	_, err := c.shim.Resume(ctx, &shim.ResumeRequest{})
	return err
}

func (c *Container) Kill(ctx context.Context, signal uint32, pid uint32, all bool) error {
	_, err := c.shim.Kill(ctx, &shim.KillRequest{
		Signal: signal,
		Pid:    pid,
		All:    all,
	})
	return err
}

func (c *Container) Exec(ctx context.Context, opts plugin.ExecOpts) (plugin.Process, error) {
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
		return nil, err

	}
	return &Process{
		pid: int(resp.Pid),
		c:   c,
	}, nil
}

func (c *Container) Processes(ctx context.Context) ([]uint32, error) {
	resp, err := c.shim.Processes(ctx, &shim.ProcessesRequest{
		ID: c.id,
	})

	if err != nil {
		return nil, err
	}

	pids := make([]uint32, 0, len(resp.Processes))

	for _, ps := range resp.Processes {
		pids = append(pids, ps.Pid)
	}

	return pids, nil
}

func (c *Container) Pty(ctx context.Context, pid uint32, size plugin.ConsoleSize) error {
	_, err := c.shim.Pty(ctx, &shim.PtyRequest{
		Pid:    pid,
		Width:  size.Width,
		Height: size.Height,
	})
	return err
}

func (c *Container) CloseStdin(ctx context.Context, pid uint32) error {
	_, err := c.shim.CloseStdin(ctx, &shim.CloseStdinRequest{
		Pid: pid,
	})
	return err
}

type Process struct {
	pid int
	c   *Container
}

func (p *Process) Kill(ctx context.Context, signal uint32, _ bool) error {
	_, err := p.c.shim.Kill(ctx, &shim.KillRequest{
		Signal: signal,
		Pid:    uint32(p.pid),
	})
	return err
}

func (p *Process) State(ctx context.Context) (plugin.State, error) {
	// use the container status for the status of the process
	state, err := p.c.State(ctx)
	if err != nil {
		return nil, err
	}
	state.(*State).pid = uint32(p.pid)
	return state, nil
}
