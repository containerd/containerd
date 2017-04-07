// +build windows

package hcs

import (
	"syscall"

	"github.com/Microsoft/hcsshim"
	"github.com/containerd/containerd"
	"github.com/pkg/errors"
)

type Process struct {
	containerID string
	p           hcsshim.Process
	status      containerd.Status
}

func (h *Process) Pid() uint32 {
	return uint32(h.p.Pid())
}

func (h *Process) Kill() error {
	return h.p.Kill()
}

func (h *Process) ExitCode() (uint32, error) {
	if err := h.p.Wait(); err != nil {
		if herr, ok := err.(*hcsshim.ProcessError); ok && herr.Err != syscall.ERROR_BROKEN_PIPE {
			return 255, errors.Wrapf(err, "failed to wait for container '%s' process %d", h.containerID, h.p.Pid())
		}
		// container is probably dead, let's try to get its exit code
	}
	h.status = containerd.StoppedStatus

	ec, err := h.p.ExitCode()
	if err != nil {
		if herr, ok := err.(*hcsshim.ProcessError); ok && herr.Err != syscall.ERROR_BROKEN_PIPE {
			return 255, errors.Wrapf(err, "failed to get container '%s' process %d exit code", h.containerID, h.p.Pid())
		}
		// Well, unknown exit code it is
		ec = 255
	}

	return uint32(ec), err
}

func (h *Process) Status() containerd.Status {
	return h.status
}
