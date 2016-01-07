package supervisor

type CreateCheckpointEvent struct {
	s *Supervisor
}

func (h *CreateCheckpointEvent) Handle(e *Task) error {
	i, ok := h.s.containers[e.ID]
	if !ok {
		return ErrContainerNotFound
	}
	return i.container.Checkpoint(*e.Checkpoint)
}

type DeleteCheckpointEvent struct {
	s *Supervisor
}

func (h *DeleteCheckpointEvent) Handle(e *Task) error {
	i, ok := h.s.containers[e.ID]
	if !ok {
		return ErrContainerNotFound
	}
	return i.container.DeleteCheckpoint(e.Checkpoint.Name)
}
