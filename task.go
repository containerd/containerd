package containerd

import (
	"context"
	"syscall"

	"github.com/containerd/containerd/api/services/execution"
	taskapi "github.com/containerd/containerd/api/types/task"
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
	Delete(context.Context) (uint32, error)
	Kill(context.Context, syscall.Signal) error
	Pause(context.Context) error
	Resume(context.Context) error
	Pid() uint32
	Start(context.Context) error
	Status(context.Context) (TaskStatus, error)
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
