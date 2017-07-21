// +build windows

package windows

import (
	"context"
	"time"

	"github.com/Microsoft/hcsshim"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/runtime"
	"github.com/pkg/errors"
)

// process implements containerd.Process and containerd.State
type process struct {
	hcs hcsshim.Process

	id     string
	pid    uint32
	io     *pipeSet
	status runtime.Status
	task   *task

	exitCh   chan struct{}
	exitCode uint32
	exitTime time.Time
}

func (p *process) ID() string {
	return p.id
}

func (p *process) State(ctx context.Context) (runtime.State, error) {
	return runtime.State{
		Status:   p.Status(),
		Pid:      p.pid,
		Stdin:    p.io.src.Stdin,
		Stdout:   p.io.src.Stdout,
		Stderr:   p.io.src.Stderr,
		Terminal: p.io.src.Terminal,
	}, nil
}

func (p *process) Status() runtime.Status {
	if p.task.getStatus() == runtime.PausedStatus {
		return runtime.PausedStatus
	}

	var status runtime.Status
	select {
	case <-p.exitCh:
		status = runtime.StoppedStatus
	default:
		status = runtime.RunningStatus
	}
	return status
}

func (p *process) Kill(ctx context.Context, sig uint32, all bool) error {
	// On windows all signals kill the process
	return errors.Wrap(p.hcs.Kill(), "failed to kill process")
}

func (p *process) ResizePty(ctx context.Context, size runtime.ConsoleSize) error {
	err := p.hcs.ResizeConsole(uint16(size.Width), uint16(size.Height))
	return errors.Wrap(err, "failed to resize process console")
}

func (p *process) CloseIO(ctx context.Context) error {
	return errors.Wrap(p.hcs.CloseStdin(), "failed to close stdin")
}

func (p *process) Pid() uint32 {
	return p.pid
}

func (p *process) ExitCode() (uint32, time.Time, error) {
	if p.Status() != runtime.StoppedStatus {
		return 255, time.Time{}, errors.Wrap(errdefs.ErrFailedPrecondition, "process is not stopped")
	}
	return p.exitCode, p.exitTime, nil
}
