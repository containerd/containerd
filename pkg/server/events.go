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
	"time"

	"github.com/containerd/containerd/api/services/events/v1"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/typeurl"
	"github.com/golang/glog"
	"github.com/jpillora/backoff"
	"golang.org/x/net/context"

	containerstore "github.com/kubernetes-incubator/cri-containerd/pkg/store/container"
)

const (
	// minRetryInterval is the minimum retry interval when lost connection with containerd.
	minRetryInterval = 100 * time.Millisecond
	// maxRetryInterval is the maximum retry interval when lost connection with containerd.
	maxRetryInterval = 30 * time.Second
	// exponentialFactor is the exponential backoff factor.
	exponentialFactor = 2.0
)

// startEventMonitor starts an event monitor which monitors and handles all
// container events.
// TODO(random-liu): [P1] Is it possible to drop event during containerd is running?
func (c *criContainerdService) startEventMonitor() {
	b := backoff.Backoff{
		Min:    minRetryInterval,
		Max:    maxRetryInterval,
		Factor: exponentialFactor,
	}
	go func() {
		for {
			eventstream, err := c.eventService.Subscribe(context.Background(), &events.SubscribeRequest{})
			if err != nil {
				glog.Errorf("Failed to connect to containerd event stream: %v", err)
				time.Sleep(b.Duration())
				continue
			}
			// Successfully connect with containerd, reset backoff.
			b.Reset()
			// TODO(random-liu): Relist to recover state, should prevent other operations
			// until state is fully recovered.
			for {
				if err := c.handleEventStream(eventstream); err != nil {
					glog.Errorf("Failed to handle event stream: %v", err)
					break
				}
			}
		}
	}()
}

// handleEventStream receives an event from containerd and handles the event.
func (c *criContainerdService) handleEventStream(eventstream events.Events_SubscribeClient) error {
	e, err := eventstream.Recv()
	if err != nil {
		return err
	}
	glog.V(4).Infof("Received container event timestamp - %v, namespace - %q, topic - %q", e.Timestamp, e.Namespace, e.Topic)
	c.handleEvent(e)
	return nil
}

// handleEvent handles a containerd event.
func (c *criContainerdService) handleEvent(evt *events.Envelope) {
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
			glog.Errorf("Failed to get container %q: %v", e.ContainerID, err)
			return
		}
		if e.Pid != cntr.Status.Get().Pid {
			// Non-init process died, ignore the event.
			return
		}
		task, err := cntr.Container.Task(context.Background(), nil)
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
		err = cntr.Status.Update(func(status containerstore.Status) (containerstore.Status, error) {
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
			glog.Errorf("Failed to get container %q: %v", e.ContainerID, err)
		}
		err = cntr.Status.Update(func(status containerstore.Status) (containerstore.Status, error) {
			status.Reason = oomExitReason
			return status, nil
		})
		if err != nil {
			glog.Errorf("Failed to update container %q oom: %v", e.ContainerID, err)
			return
		}
	}
}
