package supervisor

import (
	"os"
	"sync"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/docker/containerd/runtime"
)

// Worker interface
type Worker interface {
	Start()
}

type startTask struct {
	Container      runtime.Container
	CheckpointPath string
	Stdin          string
	Stdout         string
	Stderr         string
	Err            chan error
	StartResponse  chan StartResponse
}

// NewWorker return a new initialized worker
func NewWorker(s *Supervisor, wg *sync.WaitGroup) Worker {
	return &worker{
		s:  s,
		wg: wg,
	}
}

type worker struct {
	wg *sync.WaitGroup
	s  *Supervisor
}

// Start runs a loop in charge of starting new containers
func (w *worker) Start() {
	defer w.wg.Done()
	sendDeleteEvent := func(id string, p runtime.Process, errCh chan error, err error) {
		if p != nil {
			p.Signal(os.Kill)
			p.Wait()
		}
		logrus.WithFields(logrus.Fields{
			"error": err,
			"id":    id,
		}).Error("containerd: start container")
		errCh <- err
		evt := &DeleteTask{
			ID:      id,
			NoEvent: true,
		}
		w.s.SendTask(evt)
	}
	for t := range w.s.startTasks {
		started := time.Now()
		process, err := t.Container.Start(t.CheckpointPath, runtime.NewStdio(t.Stdin, t.Stdout, t.Stderr))
		if err != nil {
			sendDeleteEvent(t.Container.ID(), process, t.Err, err)
			continue
		}
		if err := w.s.monitor.MonitorOOM(t.Container); err != nil && err != runtime.ErrContainerExited {
			if process.State() != runtime.Stopped {
				logrus.WithField("error", err).Error("containerd: notify OOM events")
			}
		}
		if err := w.s.monitorProcess(process); err != nil {
			logrus.WithField("error", err).Error("containerd: add process to monitor")
			sendDeleteEvent(t.Container.ID(), process, t.Err, err)
			continue
		}
		if err := process.Start(); err != nil {
			logrus.WithField("error", err).Error("containerd: start init process")
			sendDeleteEvent(t.Container.ID(), process, t.Err, err)
			continue
		}
		ContainerStartTimer.UpdateSince(started)
		t.Err <- nil
		t.StartResponse <- StartResponse{
			Container: t.Container,
		}
		w.s.notifySubscribers(Event{
			Timestamp: time.Now(),
			ID:        t.Container.ID(),
			Type:      StateStart,
		})
	}
}
