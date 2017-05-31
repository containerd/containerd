package containerd

import (
	"context"
	"syscall"

	"github.com/containerd/containerd/api/services/execution"
	taskapi "github.com/containerd/containerd/api/types/task"
	specs "github.com/opencontainers/runtime-spec/specs-go"
)

const UnknownExitStatus = 255

type TaskStatus string

const (
	Running TaskStatus = "running"
	Created TaskStatus = "created"
	Stopped TaskStatus = "stopped"
	Paused  TaskStatus = "paused"
	Pausing TaskStatus = "pausing"
)

type Task interface {
	Pid() uint32
	Delete(context.Context) (uint32, error)
	Kill(context.Context, syscall.Signal) error
	Pause(context.Context) error
	Resume(context.Context) error
	Start(context.Context) error
	Status(context.Context) (TaskStatus, error)
	Wait(context.Context) (uint32, error)
	Exec(context.Context, *specs.Process, IOCreation) (Process, error)
	Processes(context.Context) ([]uint32, error)
}

type Process interface {
	Pid() uint32
	Start(context.Context) error
	Kill(context.Context, syscall.Signal) error
	Wait(context.Context) (uint32, error)
}

var _ = (Task)(&task{})

type task struct {
	client *Client

	io          *IO
	containerID string
	pid         uint32
}

// Pid returns the pid or process id for the task
func (t *task) Pid() uint32 {
	return t.pid
}

func (t *task) Start(ctx context.Context) error {
	_, err := t.client.TaskService().Start(ctx, &execution.StartRequest{
		ContainerID: t.containerID,
	})
	return err
}

func (t *task) Kill(ctx context.Context, s syscall.Signal) error {
	_, err := t.client.TaskService().Kill(ctx, &execution.KillRequest{
		Signal:      uint32(s),
		ContainerID: t.containerID,
		PidOrAll: &execution.KillRequest_All{
			All: true,
		},
	})
	return err
}

func (t *task) Pause(ctx context.Context) error {
	_, err := t.client.TaskService().Pause(ctx, &execution.PauseRequest{
		ContainerID: t.containerID,
	})
	return err
}

func (t *task) Resume(ctx context.Context) error {
	_, err := t.client.TaskService().Resume(ctx, &execution.ResumeRequest{
		ContainerID: t.containerID,
	})
	return err
}

func (t *task) Status(ctx context.Context) (TaskStatus, error) {
	r, err := t.client.TaskService().Info(ctx, &execution.InfoRequest{
		ContainerID: t.containerID,
	})
	if err != nil {
		return "", err
	}
	return TaskStatus(r.Task.Status.String()), nil
}

// Wait is a blocking call that will wait for the task to exit and return the exit status
func (t *task) Wait(ctx context.Context) (uint32, error) {
	events, err := t.client.TaskService().Events(ctx, &execution.EventsRequest{})
	if err != nil {
		return UnknownExitStatus, err
	}
	for {
		e, err := events.Recv()
		if err != nil {
			return UnknownExitStatus, err
		}
		if e.Type != taskapi.Event_EXIT {
			continue
		}
		if e.ID == t.containerID && e.Pid == t.pid {
			return e.ExitStatus, nil
		}
	}
}

// Delete deletes the task and its runtime state
// it returns the exit status of the task and any errors that were encountered
// during cleanup
func (t *task) Delete(ctx context.Context) (uint32, error) {
	cerr := t.io.Close()
	r, err := t.client.TaskService().Delete(ctx, &execution.DeleteRequest{
		ContainerID: t.containerID,
	})
	if err != nil {
		return UnknownExitStatus, err
	}
	return r.ExitStatus, cerr
}

func (t *task) Exec(ctx context.Context, spec *specs.Process, ioCreate IOCreation) (Process, error) {
	i, err := ioCreate()
	if err != nil {
		return nil, err
	}
	return &process{
		task:    t,
		io:      i,
		spec:    spec,
		pidSync: make(chan struct{}),
	}, nil
}

func (t *task) Processes(ctx context.Context) ([]uint32, error) {
	response, err := t.client.TaskService().Processes(ctx, &execution.ProcessesRequest{
		ContainerID: t.containerID,
	})
	if err != nil {
		return nil, err
	}
	var out []uint32
	for _, p := range response.Processes {
		out = append(out, p.Pid)
	}
	return out, nil
}
