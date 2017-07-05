package supervisor

import (
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/containerd/containerd/runtime"
)

// DeleteTask holds needed parameters to remove a container
type DeleteTask struct {
	baseTask
	ID      string
	Status  uint32
	PID     string
	NoEvent bool
	Process runtime.Process
}

func (s *Supervisor) delete(t *DeleteTask) error {
	if i, ok := s.containers[t.ID]; ok {
		var transactionID int64
		now := time.Now()
		if !t.NoEvent {
			transaction, err := s.transactionFactory.OpenTransaction(t.ID,
				ExitTransaction,
				WithTransactionMetadata("pid", t.PID),
				WithTransactionMetadata("status", t.Status),
				WithTransactionTimestamp(now))
			if err != nil {
				return err
			}
			transactionID = transaction.TransactionID()
		}

		if err := s.deleteContainer(i.container); err != nil {
			logrus.WithField("error", err).Error("containerd: deleting container")
		}
		if t.Process != nil {
			t.Process.Wait()
		}
		if !t.NoEvent {
			execMap := s.getExecSyncMap(t.ID)
			go func() {
				// Wait for all exec processe events to be sent (we seem
				// to sometimes receive them after the init event)
				for _, ch := range execMap {
					<-ch
				}
				s.deleteExecSyncMap(t.ID)
				s.notifySubscribers(Event{
					Type:          StateExit,
					Timestamp:     now,
					ID:            t.ID,
					Status:        t.Status,
					PID:           t.PID,
					TransactionID: transactionID,
				})
			}()
		}
		ContainersCounter.Dec(1)
		ContainerDeleteTimer.UpdateSince(now)
	}
	return nil
}

func (s *Supervisor) deleteContainer(container runtime.Container) error {
	delete(s.containers, container.ID())
	return container.Delete()
}
