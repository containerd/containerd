package supervisor

import (
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/docker/containerd/api/grpc/types"
)

type ExitEvent struct {
	s *Supervisor
}

func (h *ExitEvent) Handle(e *Event) error {
	start := time.Now()
	logrus.WithFields(logrus.Fields{"pid": e.Pid, "status": e.Status}).
		Debug("containerd: process exited")
	// is it the child process of a container
	if info, ok := h.s.processes[e.Pid]; ok {
		ne := NewEvent(types.EventType_EVENT_TYPE_EXEC_EXIT)
		ne.ID = info.container.ID()
		ne.Pid = e.Pid
		ne.Status = e.Status
		h.s.SendEvent(ne)
		return nil
	}
	// is it the main container's process
	container, err := h.s.getContainerForPid(e.Pid)
	if err != nil {
		if err != errNoContainerForPid {
			logrus.WithField("error", err).Error("containerd: find containers main pid")
		}
		return nil
	}
	container.SetExited(e.Status)
	ne := NewEvent(types.EventType_EVENT_TYPE_DELETE)
	ne.ID = container.ID()
	ne.Pid = e.Pid
	ne.Status = e.Status
	h.s.SendEvent(ne)

	stopCollect := NewEvent(types.EventType_EVENT_TYPE_STOP_STATS)
	stopCollect.ID = container.ID()
	h.s.SendEvent(stopCollect)
	ExitProcessTimer.UpdateSince(start)
	return nil
}

type ExecExitEvent struct {
	s *Supervisor
}

func (h *ExecExitEvent) Handle(e *Event) error {
	// exec process: we remove this process without notifying the main event loop
	info := h.s.processes[e.Pid]
	if err := info.container.RemoveProcess(e.Pid); err != nil {
		logrus.WithField("error", err).Error("containerd: find container for pid")
	}
	if err := info.copier.Close(); err != nil {
		logrus.WithField("error", err).Error("containerd: close process IO")
	}
	delete(h.s.processes, e.Pid)
	h.s.notifySubscribers(e)
	return nil
}
