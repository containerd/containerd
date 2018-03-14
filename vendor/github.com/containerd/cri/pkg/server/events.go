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

	eventtypes "github.com/containerd/containerd/api/events"
	containerdio "github.com/containerd/containerd/cio"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/events"
	"github.com/containerd/typeurl"
	"github.com/sirupsen/logrus"
	"golang.org/x/net/context"

	ctrdutil "github.com/containerd/cri/pkg/containerd/util"
	"github.com/containerd/cri/pkg/store"
	containerstore "github.com/containerd/cri/pkg/store/container"
	sandboxstore "github.com/containerd/cri/pkg/store/sandbox"
)

// eventMonitor monitors containerd event and updates internal state correspondingly.
// TODO(random-liu): [P1] Figure out is it possible to drop event during containerd
// is running. If it is, we should do periodically list to sync state with containerd.
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
	// event subscribe doesn't need namespace.
	ctx, cancel := context.WithCancel(context.Background())
	return &eventMonitor{
		containerStore: c,
		sandboxStore:   s,
		ctx:            ctx,
		cancel:         cancel,
	}
}

// subscribe starts to subscribe containerd events.
func (em *eventMonitor) subscribe(subscriber events.Subscriber) {
	filters := []string{
		`topic=="/tasks/exit"`,
		`topic=="/tasks/oom"`,
	}
	em.ch, em.errCh = subscriber.Subscribe(em.ctx, filters...)
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
				logrus.Debugf("Received containerd event timestamp - %v, namespace - %q, topic - %q", e.Timestamp, e.Namespace, e.Topic)
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
	ctx := ctrdutil.NamespacedContext()
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
		if err == nil {
			handleContainerExit(ctx, e, cntr)
			return
		} else if err != store.ErrNotExist {
			logrus.WithError(err).Errorf("Failed to get container %q", e.ContainerID)
			return
		}
		// Use GetAll to include sandbox in unknown state.
		sb, err := em.sandboxStore.GetAll(e.ContainerID)
		if err == nil {
			handleSandboxExit(ctx, e, sb)
			return
		} else if err != store.ErrNotExist {
			logrus.WithError(err).Errorf("Failed to get sandbox %q", e.ContainerID)
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
			return
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

// handleContainerExit handles TaskExit event for container.
func handleContainerExit(ctx context.Context, e *eventtypes.TaskExit, cntr containerstore.Container) {
	if e.Pid != cntr.Status.Get().Pid {
		// Non-init process died, ignore the event.
		return
	}
	// Attach container IO so that `Delete` could cleanup the stream properly.
	task, err := cntr.Container.Task(ctx,
		func(*containerdio.FIFOSet) (containerdio.IO, error) {
			return cntr.IO, nil
		},
	)
	if err != nil {
		if !errdefs.IsNotFound(err) {
			logrus.WithError(err).Errorf("failed to load task for container %q", e.ContainerID)
			return
		}
	} else {
		// TODO(random-liu): [P1] This may block the loop, we may want to spawn a worker
		if _, err = task.Delete(ctx); err != nil {
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
	// Using channel to propagate the information of container stop
	cntr.Stop()
}

// handleSandboxExit handles TaskExit event for sandbox.
func handleSandboxExit(ctx context.Context, e *eventtypes.TaskExit, sb sandboxstore.Sandbox) {
	if e.Pid != sb.Status.Get().Pid {
		// Non-init process died, ignore the event.
		return
	}
	// No stream attached to sandbox container.
	task, err := sb.Container.Task(ctx, nil)
	if err != nil {
		if !errdefs.IsNotFound(err) {
			logrus.WithError(err).Errorf("failed to load task for sandbox %q", e.ContainerID)
			return
		}
	} else {
		// TODO(random-liu): [P1] This may block the loop, we may want to spawn a worker
		if _, err = task.Delete(ctx); err != nil {
			// TODO(random-liu): [P0] Enqueue the event and retry.
			if !errdefs.IsNotFound(err) {
				logrus.WithError(err).Errorf("failed to stop sandbox %q", e.ContainerID)
				return
			}
			// Move on to make sure container status is updated.
		}
	}
	err = sb.Status.Update(func(status sandboxstore.Status) (sandboxstore.Status, error) {
		// NOTE(random-liu): We SHOULD NOT change UNKNOWN state here.
		// If sandbox state is UNKNOWN when event monitor receives an TaskExit event,
		// it means that sandbox start has failed. In that case, `RunPodSandbox` will
		// cleanup everything immediately.
		// Once sandbox state goes out of UNKNOWN, it becomes visable to the user, which
		// is not what we want.
		if status.State != sandboxstore.StateUnknown {
			status.State = sandboxstore.StateNotReady
		}
		status.Pid = 0
		return status, nil
	})
	if err != nil {
		logrus.WithError(err).Errorf("Failed to update sandbox %q state", e.ContainerID)
		// TODO(random-liu): [P0] Enqueue the event and retry.
		return
	}
	// Using channel to propagate the information of sandbox stop
	sb.Stop()
}
