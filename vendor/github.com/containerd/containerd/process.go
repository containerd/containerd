package containerd

import (
	"context"
	"strings"
	"syscall"
	"time"

	eventsapi "github.com/containerd/containerd/api/services/events/v1"
	"github.com/containerd/containerd/api/services/tasks/v1"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/runtime"
	"github.com/containerd/containerd/typeurl"
	"github.com/pkg/errors"
)

// Process represents a system process
type Process interface {
	// Pid is the system specific process id
	Pid() uint32
	// Start starts the process executing the user's defined binary
	Start(context.Context) error
	// Delete removes the process and any resources allocated returning the exit status
	Delete(context.Context, ...ProcessDeleteOpts) (*ExitStatus, error)
	// Kill sends the provided signal to the process
	Kill(context.Context, syscall.Signal, ...KillOpts) error
	// Wait asynchronously waits for the process to exit, and sends the exit code to the returned channel
	Wait(context.Context) (<-chan ExitStatus, error)
	// CloseIO allows various pipes to be closed on the process
	CloseIO(context.Context, ...IOCloserOpts) error
	// Resize changes the width and heigh of the process's terminal
	Resize(ctx context.Context, w, h uint32) error
	// IO returns the io set for the process
	IO() IO
	// Status returns the executing status of the process
	Status(context.Context) (Status, error)
}

// ExitStatus encapsulates a process' exit status.
// It is used by `Wait()` to return either a process exit code or an error
type ExitStatus struct {
	code     uint32
	exitedAt time.Time
	err      error
}

// Result returns the exit code and time of the exit status.
// An error may be returned here to which indicates there was an error
//   at some point while waiting for the exit status. It does not signify
//   an error with the process itself.
// If an error is returned, the process may still be running.
func (s ExitStatus) Result() (uint32, time.Time, error) {
	return s.code, s.exitedAt, s.err
}

// ExitCode returns the exit code of the process.
// This is only valid is Error() returns nil
func (s ExitStatus) ExitCode() uint32 {
	return s.code
}

// ExitTime returns the exit time of the process
// This is only valid is Error() returns nil
func (s ExitStatus) ExitTime() time.Time {
	return s.exitedAt
}

// Error returns the error, if any, that occured while waiting for the
// process.
func (s ExitStatus) Error() error {
	return s.err
}

type process struct {
	id   string
	task *task
	pid  uint32
	io   IO
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
		return errdefs.FromGRPC(err)
	}
	p.pid = r.Pid
	return nil
}

func (p *process) Kill(ctx context.Context, s syscall.Signal, opts ...KillOpts) error {
	var i KillInfo
	for _, o := range opts {
		if err := o(ctx, p, &i); err != nil {
			return err
		}
	}
	_, err := p.task.client.TaskService().Kill(ctx, &tasks.KillRequest{
		Signal:      uint32(s),
		ContainerID: p.task.id,
		ExecID:      p.id,
		All:         i.All,
	})
	return errdefs.FromGRPC(err)
}

func (p *process) Wait(ctx context.Context) (<-chan ExitStatus, error) {
	cancellable, cancel := context.WithCancel(ctx)
	eventstream, err := p.task.client.EventService().Subscribe(cancellable, &eventsapi.SubscribeRequest{
		Filters: []string{"topic==" + runtime.TaskExitEventTopic},
	})
	if err != nil {
		cancel()
		return nil, err
	}
	// first check if the task has exited
	status, err := p.Status(ctx)
	if err != nil {
		cancel()
		return nil, errdefs.FromGRPC(err)
	}

	chStatus := make(chan ExitStatus, 1)
	if status.Status == Stopped {
		cancel()
		chStatus <- ExitStatus{code: status.ExitStatus, exitedAt: status.ExitTime}
		return chStatus, nil
	}

	go func() {
		defer cancel()
		chStatus <- ExitStatus{} // signal that the goroutine is running
		for {
			evt, err := eventstream.Recv()
			if err != nil {
				chStatus <- ExitStatus{code: UnknownExitStatus, err: err}
				return
			}
			if typeurl.Is(evt.Event, &eventsapi.TaskExit{}) {
				v, err := typeurl.UnmarshalAny(evt.Event)
				if err != nil {
					chStatus <- ExitStatus{code: UnknownExitStatus, err: err}
					return
				}
				e := v.(*eventsapi.TaskExit)
				if e.ID == p.id && e.ContainerID == p.task.id {
					chStatus <- ExitStatus{code: e.ExitStatus, exitedAt: e.ExitedAt}
					return
				}
			}
		}
	}()

	<-chStatus // wait for the goroutine to be running
	return chStatus, nil
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
	return errdefs.FromGRPC(err)
}

func (p *process) IO() IO {
	return p.io
}

func (p *process) Resize(ctx context.Context, w, h uint32) error {
	_, err := p.task.client.TaskService().ResizePty(ctx, &tasks.ResizePtyRequest{
		ContainerID: p.task.id,
		Width:       w,
		Height:      h,
		ExecID:      p.id,
	})
	return errdefs.FromGRPC(err)
}

func (p *process) Delete(ctx context.Context, opts ...ProcessDeleteOpts) (*ExitStatus, error) {
	for _, o := range opts {
		if err := o(ctx, p); err != nil {
			return nil, err
		}
	}
	status, err := p.Status(ctx)
	if err != nil {
		return nil, err
	}
	switch status.Status {
	case Running, Paused, Pausing:
		return nil, errors.Wrapf(errdefs.ErrFailedPrecondition, "process must be stopped before deletion")
	}
	r, err := p.task.client.TaskService().DeleteProcess(ctx, &tasks.DeleteProcessRequest{
		ContainerID: p.task.id,
		ExecID:      p.id,
	})
	if err != nil {
		return nil, errdefs.FromGRPC(err)
	}
	if p.io != nil {
		p.io.Wait()
		p.io.Close()
	}
	return &ExitStatus{code: r.ExitStatus, exitedAt: r.ExitedAt}, nil
}

func (p *process) Status(ctx context.Context) (Status, error) {
	r, err := p.task.client.TaskService().Get(ctx, &tasks.GetRequest{
		ContainerID: p.task.id,
		ExecID:      p.id,
	})
	if err != nil {
		return Status{}, errdefs.FromGRPC(err)
	}
	return Status{
		Status:     ProcessStatus(strings.ToLower(r.Process.Status.String())),
		ExitStatus: r.Process.ExitStatus,
	}, nil
}
