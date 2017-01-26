package supervisor

import (
	"os"
	"time"

	"github.com/docker/containerd/runtime"
	"github.com/docker/containerd/specs"
	"golang.org/x/net/context"
)

// AddProcessTask holds everything necessary to add a process to a
// container
type AddProcessTask struct {
	baseTask
	ID            string
	PID           string
	Stdout        string
	Stderr        string
	Stdin         string
	ProcessSpec   *specs.ProcessSpec
	StartResponse chan StartResponse
	Ctx           context.Context
}

func (s *Supervisor) addProcess(t *AddProcessTask) error {
	start := time.Now()
	ci, ok := s.containers[t.ID]
	if !ok {
		return ErrContainerNotFound
	}
	process, err := ci.container.Exec(t.Ctx, t.PID, *t.ProcessSpec, runtime.NewStdio(t.Stdin, t.Stdout, t.Stderr))
	if err != nil {
		return err
	}
	s.newExecSyncChannel(t.ID, t.PID)
	if err := s.monitorProcess(process); err != nil {
		s.deleteExecSyncChannel(t.ID, t.PID)
		// Kill process
		process.Signal(os.Kill)
		ci.container.RemoveProcess(t.PID)
		return err
	}
	ExecProcessTimer.UpdateSince(start)
	t.StartResponse <- StartResponse{ExecPid: process.SystemPid()}
	s.notifySubscribers(Event{
		Timestamp: time.Now(),
		Type:      StateStartProcess,
		PID:       t.PID,
		ID:        t.ID,
	})
	return nil
}
