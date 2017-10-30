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
	"github.com/containerd/containerd"
	"github.com/containerd/containerd/api/services/events/v1"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/typeurl"
	"github.com/golang/glog"
	"golang.org/x/net/context"

	containerstore "github.com/kubernetes-incubator/cri-containerd/pkg/store/container"
)

// eventMonitor monitors containerd event and updates internal state correspondingly.
// TODO(random-liu): [P1] Is it possible to drop event during containerd is running?
type eventMonitor struct {
	c       *criContainerdService
	ch      <-chan *events.Envelope
	errCh   <-chan error
	closeCh chan struct{}
	cancel  context.CancelFunc
}

// Create new event monitor. New event monitor will start subscribing containerd event. All events
// happen after it should be monitored.
func newEventMonitor(c *criContainerdService) *eventMonitor {
	ctx, cancel := context.WithCancel(context.Background())
	ch, errCh := c.client.Subscribe(ctx)
	return &eventMonitor{
		c:       c,
		ch:      ch,
		errCh:   errCh,
		closeCh: make(chan struct{}),
		cancel:  cancel,
	}
}

// start starts the event monitor which monitors and handles all container events. It returns
// a channel for the caller to wait for the event monitor to stop.
func (em *eventMonitor) start() <-chan struct{} {
	go func() {
		for {
			select {
			case e := <-em.ch:
				glog.V(4).Infof("Received container event timestamp - %v, namespace - %q, topic - %q", e.Timestamp, e.Namespace, e.Topic)
				em.handleEvent(e)
			case err := <-em.errCh:
				glog.Errorf("Failed to handle event stream: %v", err)
				close(em.closeCh)
				return
			}
		}
	}()
	return em.closeCh
}

// stop stops the event monitor. It will close the event channel.
func (em *eventMonitor) stop() {
	em.cancel()
}

// handleEvent handles a containerd event.
func (em *eventMonitor) handleEvent(evt *events.Envelope) {
	c := em.c
	any, err := typeurl.UnmarshalAny(evt.Event)
	if err != nil {
		glog.Errorf("Failed to convert event envelope %+v: %v", evt, err)
		return
	}
	switch any.(type) {
	// If containerd-shim exits unexpectedly, there will be no corresponding event.
	// However, containerd could not retrieve container state in that case, so it's
	// fine to leave out that case for now.
	// TODO(random-liu): [P2] Handle containerd-shim exit.
	case *events.TaskExit:
		e := any.(*events.TaskExit)
		glog.V(2).Infof("TaskExit event %+v", e)
		cntr, err := c.containerStore.Get(e.ContainerID)
		if err != nil {
			if _, err := c.sandboxStore.Get(e.ContainerID); err == nil {
				return
			}
			glog.Errorf("Failed to get container %q: %v", e.ContainerID, err)
			return
		}
		if e.Pid != cntr.Status.Get().Pid {
			// Non-init process died, ignore the event.
			return
		}
		// Attach container IO so that `Delete` could cleanup the stream properly.
		task, err := cntr.Container.Task(context.Background(),
			func(*containerd.FIFOSet) (containerd.IO, error) {
				return cntr.IO, nil
			},
		)
		if err != nil {
			if !errdefs.IsNotFound(err) {
				glog.Errorf("failed to stop container, task not found for container %q: %v", e.ContainerID, err)
				return
			}
		} else {
			// TODO(random-liu): [P1] This may block the loop, we may want to spawn a worker
			if _, err = task.Delete(context.Background()); err != nil {
				// TODO(random-liu): [P0] Enqueue the event and retry.
				if !errdefs.IsNotFound(err) {
					glog.Errorf("failed to stop container %q: %v", e.ContainerID, err)
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
			glog.Errorf("Failed to update container %q state: %v", e.ContainerID, err)
			// TODO(random-liu): [P0] Enqueue the event and retry.
			return
		}
	case *events.TaskOOM:
		e := any.(*events.TaskOOM)
		glog.V(2).Infof("TaskOOM event %+v", e)
		cntr, err := c.containerStore.Get(e.ContainerID)
		if err != nil {
			if _, err := c.sandboxStore.Get(e.ContainerID); err == nil {
				return
			}
			glog.Errorf("Failed to get container %q: %v", e.ContainerID, err)
		}
		err = cntr.Status.UpdateSync(func(status containerstore.Status) (containerstore.Status, error) {
			status.Reason = oomExitReason
			return status, nil
		})
		if err != nil {
			glog.Errorf("Failed to update container %q oom: %v", e.ContainerID, err)
			return
		}
	}
}
