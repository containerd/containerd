/*
Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package server

import (
	"errors"

	"github.com/containerd/containerd"
	eventtypes "github.com/containerd/containerd/api/events"
	"github.com/containerd/containerd/api/services/events/v1"
	containerdio "github.com/containerd/containerd/cio"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/typeurl"
	"github.com/sirupsen/logrus"
	"golang.org/x/net/context"

	containerstore "github.com/containerd/cri-containerd/pkg/store/container"
	sandboxstore "github.com/containerd/cri-containerd/pkg/store/sandbox"
)

// eventMonitor monitors containerd event and updates internal state correspondingly.
// TODO(random-liu): [P1] Is it possible to drop event during containerd is running?
type eventMonitor struct {
	containerStore *containerstore.Store
	sandboxStore   *sandboxstore.Store
	ch             <-chan *events.Envelope
	errCh          <-chan error
	ctx            context.Context
	cancel         context.CancelFunc
}

// Create new event monitor. New event monitor will start subscribing containerd event. All events
// happen after it should be monitored.
func newEventMonitor(c *containerstore.Store, s *sandboxstore.Store) *eventMonitor {
	ctx, cancel := context.WithCancel(context.Background())
	return &eventMonitor{
		containerStore: c,
		sandboxStore:   s,
		ctx:            ctx,
		cancel:         cancel,
	}
}

// subscribe starts subsribe containerd events. We separate subscribe from
func (em *eventMonitor) subscribe(client *containerd.Client) {
	em.ch, em.errCh = client.Subscribe(em.ctx)
}

// start starts the event monitor which monitors and handles all container events. It returns
// a channel for the caller to wait for the event monitor to stop. start must be called after
// subscribe.
func (em *eventMonitor) start() (<-chan struct{}, error) {
	if em.ch == nil || em.errCh == nil {
		return nil, errors.New("event channel is nil")
	}
	closeCh := make(chan struct{})
	go func() {
		for {
			select {
			case e := <-em.ch:
				logrus.Debugf("Received container event timestamp - %v, namespace - %q, topic - %q", e.Timestamp, e.Namespace, e.Topic)
				em.handleEvent(e)
			case err := <-em.errCh:
				logrus.WithError(err).Error("Failed to handle event stream")
				close(closeCh)
				return
			}
		}
	}()
	return closeCh, nil
}

// stop stops the event monitor. It will close the event channel.
// Once event monitor is stopped, it can't be started.
func (em *eventMonitor) stop() {
	em.cancel()
}

// handleEvent handles a containerd event.
func (em *eventMonitor) handleEvent(evt *events.Envelope) {
	any, err := typeurl.UnmarshalAny(evt.Event)
	if err != nil {
		logrus.WithError(err).Errorf("Failed to convert event envelope %+v", evt)
		return
	}
	switch any.(type) {
	// If containerd-shim exits unexpectedly, there will be no corresponding event.
	// However, containerd could not retrieve container state in that case, so it's
	// fine to leave out that case for now.
	// TODO(random-liu): [P2] Handle containerd-shim exit.
	case *eventtypes.TaskExit:
		e := any.(*eventtypes.TaskExit)
		logrus.Infof("TaskExit event %+v", e)
		cntr, err := em.containerStore.Get(e.ContainerID)
		if err != nil {
			if _, err := em.sandboxStore.Get(e.ContainerID); err == nil {
				return
			}
			logrus.WithError(err).Errorf("Failed to get container %q", e.ContainerID)
			return
		}
		if e.Pid != cntr.Status.Get().Pid {
			// Non-init process died, ignore the event.
			return
		}
		// Attach container IO so that `Delete` could cleanup the stream properly.
		task, err := cntr.Container.Task(context.Background(),
			func(*containerdio.FIFOSet) (containerdio.IO, error) {
				return cntr.IO, nil
			},
		)
		if err != nil {
			if !errdefs.IsNotFound(err) {
				logrus.WithError(err).Errorf("failed to stop container, task not found for container %q", e.ContainerID)
				return
			}
		} else {
			// TODO(random-liu): [P1] This may block the loop, we may want to spawn a worker
			if _, err = task.Delete(context.Background()); err != nil {
				// TODO(random-liu): [P0] Enqueue the event and retry.
				if !errdefs.IsNotFound(err) {
					logrus.WithError(err).Errorf("failed to stop container %q", e.ContainerID)
					return
				}
				// Move on to make sure container status is updated.
			}
		}
		err = cntr.Status.UpdateSync(func(status containerstore.Status) (containerstore.Status, error) {
			// If FinishedAt has been set (e.g. with start failure), keep as
			// it is.
			if status.FinishedAt != 0 {
				return status, nil
			}
			status.Pid = 0
			status.FinishedAt = e.ExitedAt.UnixNano()
			status.ExitCode = int32(e.ExitStatus)
			return status, nil
		})
		if err != nil {
			logrus.WithError(err).Errorf("Failed to update container %q state", e.ContainerID)
			// TODO(random-liu): [P0] Enqueue the event and retry.
			return
		}
	case *eventtypes.TaskOOM:
		e := any.(*eventtypes.TaskOOM)
		logrus.Infof("TaskOOM event %+v", e)
		cntr, err := em.containerStore.Get(e.ContainerID)
		if err != nil {
			if _, err := em.sandboxStore.Get(e.ContainerID); err == nil {
				return
			}
			logrus.WithError(err).Errorf("Failed to get container %q", e.ContainerID)
		}
		err = cntr.Status.UpdateSync(func(status containerstore.Status) (containerstore.Status, error) {
			status.Reason = oomExitReason
			return status, nil
		})
		if err != nil {
			logrus.WithError(err).Errorf("Failed to update container %q oom", e.ContainerID)
			return
		}
	}
}
