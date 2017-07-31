package containerd

import (
	"context"
	"strings"
	"syscall"

	eventsapi "github.com/containerd/containerd/api/services/events/v1"
	"github.com/containerd/containerd/api/services/tasks/v1"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/runtime"
	"github.com/containerd/containerd/typeurl"
	specs "github.com/opencontainers/runtime-spec/specs-go"
)

type process struct {
	id   string
	task *task
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
	r, err := p.task.client.TaskService().Start(ctx, &tasks.StartRequest{
		ContainerID: p.task.id,
		ExecID:      p.id,
	})
	if err != nil {
		p.io.Cancel()
		p.io.Wait()
		p.io.Close()
		return err
	}
	p.pid = r.Pid
	return nil
}

func (p *process) Kill(ctx context.Context, s syscall.Signal) error {
	_, err := p.task.client.TaskService().Kill(ctx, &tasks.KillRequest{
		Signal:      uint32(s),
		ContainerID: p.task.id,
		ExecID:      p.id,
	})
	return err
}

func (p *process) Wait(ctx context.Context) (uint32, error) {
	eventstream, err := p.task.client.EventService().Subscribe(ctx, &eventsapi.SubscribeRequest{
		Filters: []string{"topic==" + runtime.TaskExitEventTopic},
	})
	if err != nil {
		return UnknownExitStatus, err
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
	_, err := p.task.client.TaskService().CloseIO(ctx, r)
	return err
}

func (p *process) IO() *IO {
	return p.io
}

func (p *process) Resize(ctx context.Context, w, h uint32) error {
	_, err := p.task.client.TaskService().ResizePty(ctx, &tasks.ResizePtyRequest{
		ContainerID: p.task.id,
		Width:       w,
		Height:      h,
		ExecID:      p.id,
	})
	return err
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
		return UnknownExitStatus, err
	}
	return r.ExitStatus, nil
}

func (p *process) Status(ctx context.Context) (TaskStatus, error) {
	r, err := p.task.client.TaskService().Get(ctx, &tasks.GetRequest{
		ContainerID: p.task.id,
		ExecID:      p.id,
	})
	if err != nil {
		return "", errdefs.FromGRPC(err)
	}
	return TaskStatus(strings.ToLower(r.Process.Status.String())), nil
}
