package containerd

import (
	"context"
	"encoding/json"
	"syscall"

	eventsapi "github.com/containerd/containerd/api/services/events/v1"
	"github.com/containerd/containerd/api/services/tasks/v1"
	"github.com/containerd/containerd/events"
	protobuf "github.com/gogo/protobuf/types"
	specs "github.com/opencontainers/runtime-spec/specs-go"
)

type process struct {
	task *task

	// this is a hack to make a blocking Wait work
	// exec does not have a create/start split so if a quick exiting process like `exit 1`
	// run, the wait does not have enough time to get the pid catch the event.  So we need
	// to lock this on process struct create and only unlock it after the pid is set
	// this allow the wait to be called before calling process start and not race with the exit event
	pidSync chan struct{}

	io   *IO
	pid  uint32
	spec *specs.Process
}

// Pid returns the pid of the process
// The pid is not set until start is called and returns
func (p *process) Pid() uint32 {
	return p.pid
}

// Start starts the exec process
func (p *process) Start(ctx context.Context) error {
	data, err := json.Marshal(p.spec)
	if err != nil {
		return err
	}
	request := &tasks.ExecProcessRequest{
		ContainerID: p.task.containerID,
		Terminal:    p.io.Terminal,
		Stdin:       p.io.Stdin,
		Stdout:      p.io.Stdout,
		Stderr:      p.io.Stderr,
		Spec: &protobuf.Any{
			TypeUrl: specs.Version,
			Value:   data,
		},
	}
	response, err := p.task.client.TaskService().Exec(ctx, request)
	if err != nil {
		return err
	}
	p.pid = response.Pid
	close(p.pidSync)
	return nil
}

func (p *process) Kill(ctx context.Context, s syscall.Signal) error {
	_, err := p.task.client.TaskService().Kill(ctx, &tasks.KillRequest{
		Signal:      uint32(s),
		ContainerID: p.task.containerID,
		PidOrAll: &tasks.KillRequest_Pid{
			Pid: p.pid,
		},
	})
	return err
}

func (p *process) Wait(ctx context.Context) (uint32, error) {
	// TODO (ehazlett): add filtering for specific event
	eventstream, err := p.task.client.EventService().Stream(ctx, &eventsapi.StreamEventsRequest{})
	if err != nil {
		return UnknownExitStatus, err
	}
	<-p.pidSync
evloop:
	for {
		evt, err := eventstream.Recv()
		if err != nil {
			return UnknownExitStatus, err
		}

		switch {
		case events.Is(evt.Event, &eventsapi.RuntimeEvent{}):
			var e eventsapi.RuntimeEvent
			if err := events.UnmarshalEvent(evt.Event, &e); err != nil {
				return UnknownExitStatus, err
			}

			if e.Type != eventsapi.RuntimeEvent_EXIT {
				continue evloop
			}

			if e.ID == p.task.containerID && e.Pid == p.pid {
				return e.ExitStatus, nil
			}
		}
	}
}

func (p *process) CloseIO(ctx context.Context, opts ...IOCloserOpts) error {
	r := &tasks.CloseIORequest{
		ContainerID: p.task.containerID,
		Pid:         p.pid,
	}
	for _, o := range opts {
		o(r)
	}
	_, err := p.task.client.TaskService().CloseIO(ctx, r)
	return err
}

func (p *process) IO() *IO {
	return p.io
}

func (p *process) Resize(ctx context.Context, w, h uint32) error {
	_, err := p.task.client.TaskService().ResizePty(ctx, &tasks.ResizePtyRequest{
		ContainerID: p.task.containerID,
		Width:       w,
		Height:      h,
		Pid:         p.pid,
	})
	return err
}

func (p *process) Delete(ctx context.Context) (uint32, error) {
	cerr := p.io.Close()
	r, err := p.task.client.TaskService().DeleteProcess(ctx, &tasks.DeleteProcessRequest{
		ContainerID: p.task.containerID,
		Pid:         p.pid,
	})
	if err != nil {
		return UnknownExitStatus, err
	}
	return r.ExitStatus, cerr
}
