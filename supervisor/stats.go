package supervisor

type StatsEvent struct {
	s *Supervisor
}

type UnsubscribeStatsEvent struct {
	s *Supervisor
}

type StopStatsEvent struct {
	s *Supervisor
}

func (h *StatsEvent) Handle(e *Task) error {
	i, ok := h.s.containers[e.ID]
	if !ok {
		return ErrContainerNotFound
	}
	e.Stats = h.s.statsCollector.collect(i.container)
	return nil
}

func (h *UnsubscribeStatsEvent) Handle(e *Task) error {
	i, ok := h.s.containers[e.ID]
	if !ok {
		return ErrContainerNotFound
	}
	h.s.statsCollector.unsubscribe(i.container, e.Stats)
	return nil
}

func (h *StopStatsEvent) Handle(e *Task) error {
	i, ok := h.s.containers[e.ID]
	if !ok {
		return ErrContainerNotFound
	}
	h.s.statsCollector.stopCollection(i.container)
	return nil
}
