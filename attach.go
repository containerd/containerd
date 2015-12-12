package containerd

type AttachEvent struct {
	s *Supervisor
}

func (h *AttachEvent) Handle(e *Event) error {
	for _, i := range h.s.containers {
		e.Containers = append(e.Containers, i.container)
	}
	return nil
}
