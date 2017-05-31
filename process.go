package containerd

import (
	"context"
	"encoding/json"
	"syscall"

	"github.com/containerd/containerd/api/services/execution"
	taskapi "github.com/containerd/containerd/api/types/task"
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
	request := &execution.ExecRequest{
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
	_, err := p.task.client.TaskService().Kill(ctx, &execution.KillRequest{
		Signal:      uint32(s),
		ContainerID: p.task.containerID,
		PidOrAll: &execution.KillRequest_Pid{
			Pid: p.pid,
		},
	})
	return err
}

func (p *process) Wait(ctx context.Context) (uint32, error) {
	events, err := p.task.client.TaskService().Events(ctx, &execution.EventsRequest{})
	if err != nil {
		return UnknownExitStatus, err
	}
	<-p.pidSync
	for {
		e, err := events.Recv()
		if err != nil {
			return UnknownExitStatus, err
		}
		if e.Type != taskapi.Event_EXIT {
			continue
		}
		if e.ID == p.task.containerID && e.Pid == p.pid {
			return e.ExitStatus, nil
		}
	}
}
