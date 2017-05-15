package supervisor

import (
	"strings"
	"time"

	"github.com/containerd/containerd/runtime"
)

// UpdateTask holds needed parameters to update a container resource constraints
type UpdateTask struct {
	baseTask
	ID        string
	State     runtime.State
	Resources *runtime.Resource
}

func (s *Supervisor) updateContainer(t *UpdateTask) error {
	i, ok := s.containers[t.ID]
	if !ok {
		return ErrContainerNotFound
	}
	container := i.container
	if t.State != "" {
		switch t.State {
		case runtime.Running:
			now := time.Now()
			transaction, err := s.transactionFactory.OpenTransaction(t.ID, ResumeTransaction, WithTransactionTimestamp(now))
			if err != nil {
				return err
			}

			if err := container.Resume(); err != nil {
				transaction.Close()
				return err
			}
			s.notifySubscribers(Event{
				ID:            t.ID,
				Type:          StateResume,
				Timestamp:     time.Now(),
				TransactionID: transaction.TransactionID(),
			})
		case runtime.Paused:
			now := time.Now()
			transaction, err := s.transactionFactory.OpenTransaction(t.ID, PauseTransaction, WithTransactionTimestamp(now))
			if err != nil {
				return err
			}

			if err := container.Pause(); err != nil {
				transaction.Close()
				return err
			}
			s.notifySubscribers(Event{
				ID:            t.ID,
				Type:          StatePause,
				Timestamp:     now,
				TransactionID: transaction.TransactionID(),
			})
		default:
			return ErrUnknownContainerStatus
		}
		return nil
	}
	if t.Resources != nil {
		return container.UpdateResources(t.Resources)
	}
	return nil
}

// HandlePauseTransaction handles the container pause transaction
func (s *Supervisor) HandlePauseTransaction(transaction Transaction) error {
	c, ok := s.containers[transaction.ContainerID()]
	if !ok {
		return ErrContainerNotFound
	}

	if err := c.container.Pause(); err != nil && !strings.Contains(err.Error(), "container not running or created: paused") {
		return err
	}

	s.notifySubscribers(Event{
		ID:        transaction.ContainerID(),
		Type:      StatePause,
		Timestamp: transaction.TimeStamp(),
	})

	return nil
}

// HandleResumeTransaction handles the container resume transaction
func (s *Supervisor) HandleResumeTransaction(transaction Transaction) error {
	c, ok := s.containers[transaction.ContainerID()]
	if !ok {
		return ErrContainerNotFound
	}
	if err := c.container.Resume(); err != nil && !strings.Contains(err.Error(), "container not paused") {
		return err
	}
	s.notifySubscribers(Event{
		ID:        transaction.ContainerID(),
		Type:      StateResume,
		Timestamp: transaction.TimeStamp(),
	})

	return nil
}

// UpdateProcessTask holds needed parameters to update a container
// process terminal size or close its stdin
type UpdateProcessTask struct {
	baseTask
	ID         string
	PID        string
	CloseStdin bool
	Width      int
	Height     int
}

func (s *Supervisor) updateProcess(t *UpdateProcessTask) error {
	i, ok := s.containers[t.ID]
	if !ok {
		return ErrContainerNotFound
	}
	processes, err := i.container.Processes()
	if err != nil {
		return err
	}
	var process runtime.Process
	for _, p := range processes {
		if p.ID() == t.PID {
			process = p
			break
		}
	}
	if process == nil {
		return ErrProcessNotFound
	}
	if t.CloseStdin {
		if err := process.CloseStdin(); err != nil {
			return err
		}
	}
	if t.Width > 0 || t.Height > 0 {
		if err := process.Resize(t.Width, t.Height); err != nil {
			return err
		}
	}
	return nil
}
