package supervisor

type StatsEvent struct {
	s *Supervisor
}

func (h *StatsEvent) Handle(e *Event) error {
	i, ok := h.s.containers[e.ID]
	if !ok {
		return ErrContainerNotFound
	}
	st, err := i.container.Stats()
	if err != nil {
		return err
	}
	e.Stats = st
	return nil
}
