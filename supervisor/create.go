package supervisor

import "time"

type StartEvent struct {
	s *Supervisor
}

func (h *StartEvent) Handle(e *Task) error {
	start := time.Now()
	container, io, err := h.s.runtime.Create(e.ID, e.BundlePath, e.Console)
	if err != nil {
		return err
	}
	h.s.containerGroup.Add(1)
	h.s.containers[e.ID] = &containerInfo{
		container: container,
	}
	ContainersCounter.Inc(1)
	task := &StartTask{
		Err:           e.Err,
		IO:            io,
		Container:     container,
		Stdin:         e.Stdin,
		Stdout:        e.Stdout,
		Stderr:        e.Stderr,
		StartResponse: e.StartResponse,
	}
	if e.Checkpoint != nil {
		task.Checkpoint = e.Checkpoint.Name
	}
	h.s.tasks <- task
	ContainerCreateTimer.UpdateSince(start)
	return errDeferedResponse
}
