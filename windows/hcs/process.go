// +build windows

package hcs

import (
	"syscall"

	"github.com/Microsoft/hcsshim"
	"github.com/containerd/containerd"
	"github.com/pkg/errors"
)

type Process struct {
	hcsshim.Process

	pid    uint32
	io     *IO
	ec     uint32
	ecErr  error
	ecSync chan struct{}
}

func (p *Process) Pid() uint32 {
	return p.pid
}

func (p *Process) ExitCode() (uint32, error) {
	<-p.ecSync
	return p.ec, p.ecErr
}

func (p *Process) Status() containerd.Status {
	select {
	case <-p.ecSync:
		return containerd.StoppedStatus
	default:
	}

	return containerd.RunningStatus
}

func (p *Process) Delete() error {
	p.io.Close()
	return p.Close()
}

func processExitCode(containerID string, p *Process) (uint32, error) {
	if err := p.Wait(); err != nil {
		if herr, ok := err.(*hcsshim.ProcessError); ok && herr.Err != syscall.ERROR_BROKEN_PIPE {
			return 255, errors.Wrapf(err, "failed to wait for container '%s' process %u", containerID, p.pid)
		}
		// process is probably dead, let's try to get its exit code
	}

	ec, err := p.Process.ExitCode()
	if err != nil {
		if herr, ok := err.(*hcsshim.ProcessError); ok && herr.Err != syscall.ERROR_BROKEN_PIPE {
			return 255, errors.Wrapf(err, "failed to get container '%s' process %d exit code", containerID, p.pid)
		}
		// Well, unknown exit code it is
		ec = 255
	}

	return uint32(ec), err
}
