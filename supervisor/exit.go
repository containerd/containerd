package supervisor

import (
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/docker/containerd/runtime"
)

// ExitTask holds needed parameters to execute the exit task
type ExitTask struct {
	baseTask
	Process runtime.Process
}

func (s *Supervisor) exit(t *ExitTask) error {
	start := time.Now()
	proc := t.Process
	status, err := proc.ExitStatus()
	if err != nil {
		logrus.WithFields(logrus.Fields{
			"error":     err,
			"pid":       proc.ID(),
			"id":        proc.Container().ID(),
			"systemPid": proc.SystemPid(),
		}).Error("containerd: get exit status")
	}
	logrus.WithFields(logrus.Fields{
		"pid":       proc.ID(),
		"status":    status,
		"id":        proc.Container().ID(),
		"systemPid": proc.SystemPid(),
	}).Debug("containerd: process exited")

	// if the process is the the init process of the container then
	// fire a separate event for this process
	if proc.ID() != runtime.InitProcessID {
		ne := &ExecExitTask{
			ID:      proc.Container().ID(),
			PID:     proc.ID(),
			Status:  status,
			Process: proc,
		}
		s.execExit(ne)
		return nil
	}
	container := proc.Container()
	ne := &DeleteTask{
		ID:        container.ID(),
		Status:    status,
		PID:       proc.ID(),
		Process:   proc,
		Container: container,
	}
	go s.waitExecProcesses(ne)

	ExitProcessTimer.UpdateSince(start)

	return nil
}

func (s *Supervisor) waitExecProcesses(ne *DeleteTask) {
	// wait for all exec processes to stop before deleting the container
	procs, err := ne.Container.Processes()
	if err != nil {
		logrus.WithField("error", err).Error("containerd: list container processes")
	}
	for _, p := range procs {
		if p.ID() != runtime.InitProcessID {
			p.WaitExit()
		}
	}
	s.SendTask(ne)
}

// ExecExitTask holds needed parameters to execute the exec exit task
type ExecExitTask struct {
	baseTask
	ID      string
	PID     string
	Status  int
	Process runtime.Process
}

func (s *Supervisor) execExit(t *ExecExitTask) error {
	container := t.Process.Container()
	// exec process: we remove this process without notifying the main event loop
	if err := container.RemoveProcess(t.PID); err != nil {
		logrus.WithField("error", err).Error("containerd: find container for pid")
	}
	t.Process.Wait()
	s.notifySubscribers(Event{
		Timestamp: time.Now(),
		ID:        t.ID,
		Type:      StateExit,
		PID:       t.PID,
		Status:    t.Status,
	})
	return nil
}
