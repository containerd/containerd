package containerd

// AttachEvent builds up the attachment to IO on the specified container. If
// PID is set, it will select the container process with the given PID.
// Otherwise, it will select the container process with PID=1.
type AttachEvent struct {
	s *Supervisor
}

func (h *AttachEvent) Handle(e *Event) error {
	container, ok := h.s.containers[e.ID]
	if !ok {
		return ErrContainerNotFound
	}

	if e.Pid != 0 {
		// TODO(stevvooe): Support lookup of process by PID.
		return ErrProcessNotFound
	} else {
		e.Pid = 1
	}

	processess, err := container.container.Processes()
	if err != nil {
		return err
	}

	// assume that PID=1 is first process. probably a terrible assumption.
	process := processess[0]

	io, err := process.IO()
	if err != nil {
		return err
	}

	e.IO = io

	return nil
}
