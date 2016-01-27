package supervisor

type GetContainersEvent struct {
	s *Supervisor
}

func (h *GetContainersEvent) Handle(e *Task) error {
	for _, i := range h.s.containers {
		e.Containers = append(e.Containers, i.container)
	}
	return nil
}
