package containerd

import (
	"context"
	"sync"
	"syscall"

	eventsapi "github.com/containerd/containerd/api/services/events/v1"
	"github.com/containerd/containerd/api/services/tasks/v1"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/typeurl"
	specs "github.com/opencontainers/runtime-spec/specs-go"
)

type Process interface {
	Pid() uint32
	Start(context.Context) error
	Delete(context.Context) (uint32, error)
	Kill(context.Context, syscall.Signal) error
	Wait(context.Context) (uint32, error)
	CloseIO(context.Context, ...IOCloserOpts) error
	Resize(ctx context.Context, w, h uint32) error
	IO() *IO
}

type process struct {
	id   string
	task *task
	mu   sync.Mutex
	pid  uint32
	io   *IO
	spec *specs.Process
}

func (p *process) ID() string {
	return p.id
}

// Pid returns the pid of the process
// The pid is not set until start is called and returns
func (p *process) Pid() uint32 {
	return p.pid
}

// Start starts the exec process
func (p *process) Start(ctx context.Context) error {
	any, err := typeurl.MarshalAny(p.spec)
	if err != nil {
		return err
	}
	request := &tasks.ExecProcessRequest{
		ContainerID: p.task.id,
		ExecID:      p.id,
		Terminal:    p.io.Terminal,
		Stdin:       p.io.Stdin,
		Stdout:      p.io.Stdout,
		Stderr:      p.io.Stderr,
		Spec:        any,
	}
	response, err := p.task.client.TaskService().Exec(ctx, request)
	if err != nil {
		p.io.Cancel()
		p.io.Wait()
		p.io.Close()
		return errdefs.FromGRPC(err)
	}
	p.mu.Lock()
	p.pid = response.Pid
	p.mu.Unlock()
	return nil
}

// Kill sends the provided Signal to the process
func (p *process) Kill(ctx context.Context, s syscall.Signal) error {
	if _, err := p.task.client.TaskService().Kill(ctx, &tasks.KillRequest{
		Signal:      uint32(s),
		ContainerID: p.task.id,
		ExecID:      p.id,
	}); err != nil {
		return errdefs.FromGRPC(err)
	}
	return nil
}

// Wait is a blocking call that will wait for the process to exit and return the exit status
//
// Wait returns an ErrUnavailable when the task has already run and stopped
func (p *process) Wait(ctx context.Context) (uint32, error) {
	eventstream, err := p.task.client.EventService().Subscribe(ctx, &eventsapi.SubscribeRequest{})
	if err != nil {
		return UnknownExitStatus, err
	}
	// first check if the process has exited only if we have a pid
	p.mu.Lock()
	started := p.pid != 0
	p.mu.Unlock()
	if started {
		if err := p.Kill(ctx, 0); err != nil {
			return UnknownExitStatus, errdefs.ErrUnavailable
		}
	}
	for {
		evt, err := eventstream.Recv()
		if err != nil {
			return UnknownExitStatus, err
		}
		if typeurl.Is(evt.Event, &eventsapi.TaskExit{}) {
			v, err := typeurl.UnmarshalAny(evt.Event)
			if err != nil {
				return UnknownExitStatus, err
			}
			e := v.(*eventsapi.TaskExit)
			if e.ID == p.id && e.ContainerID == p.task.id {
				return e.ExitStatus, nil
			}
		}
	}
}

func (p *process) CloseIO(ctx context.Context, opts ...IOCloserOpts) error {
	r := &tasks.CloseIORequest{
		ContainerID: p.task.id,
		ExecID:      p.id,
	}
	var i IOCloseInfo
	for _, o := range opts {
		o(&i)
	}
	r.Stdin = i.Stdin
	if _, err := p.task.client.TaskService().CloseIO(ctx, r); err != nil {
		return errdefs.FromGRPC(err)
	}
	return nil
}

func (p *process) IO() *IO {
	return p.io
}

func (p *process) Resize(ctx context.Context, w, h uint32) error {
	if _, err := p.task.client.TaskService().ResizePty(ctx, &tasks.ResizePtyRequest{
		ContainerID: p.task.id,
		Width:       w,
		Height:      h,
		ExecID:      p.id,
	}); err != nil {
		return errdefs.FromGRPC(err)
	}
	return nil
}

func (p *process) Delete(ctx context.Context) (uint32, error) {
	if p.io != nil {
		p.io.Wait()
		p.io.Close()
	}
	r, err := p.task.client.TaskService().DeleteProcess(ctx, &tasks.DeleteProcessRequest{
		ContainerID: p.task.id,
		ExecID:      p.id,
	})
	if err != nil {
		return UnknownExitStatus, errdefs.FromGRPC(err)
	}
	return r.ExitStatus, nil
}
