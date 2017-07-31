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

	"github.com/containerd/containerd/api/services/execution"
	"github.com/containerd/containerd/api/types/task"
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
			events, err := c.taskService.Events(context.Background(), &execution.EventsRequest{})
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
				if err := c.handleEventStream(events); err != nil {
					glog.Errorf("Failed to handle event stream: %v", err)
					break
				}
			}
		}
	}()
}

// handleEventStream receives an event from containerd and handles the event.
func (c *criContainerdService) handleEventStream(events execution.Tasks_EventsClient) error {
	e, err := events.Recv()
	if err != nil {
		return err
	}
	glog.V(2).Infof("Received container event: %+v", e)
	c.handleEvent(e)
	return nil
}

// handleEvent handles a containerd event.
func (c *criContainerdService) handleEvent(e *task.Event) {
	switch e.Type {
	// If containerd-shim exits unexpectedly, there will be no corresponding event.
	// However, containerd could not retrieve container state in that case, so it's
	// fine to leave out that case for now.
	// TODO(random-liu): [P2] Handle containerd-shim exit.
	case task.Event_EXIT:
		cntr, err := c.containerStore.Get(e.ID)
		if err != nil {
			glog.Errorf("Failed to get container %q: %v", e.ID, err)
			return
		}
		if e.Pid != cntr.Status.Get().Pid {
			// Non-init process died, ignore the event.
			return
		}
		// Delete the container from containerd.
		_, err = c.taskService.Delete(context.Background(), &execution.DeleteRequest{ContainerID: e.ID})
		if err != nil && !isContainerdGRPCNotFoundError(err) {
			// TODO(random-liu): [P0] Enqueue the event and retry.
			glog.Errorf("Failed to delete container %q: %v", e.ID, err)
			return
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
			glog.Errorf("Failed to update container %q state: %v", e.ID, err)
			// TODO(random-liu): [P0] Enqueue the event and retry.
			return
		}
	case task.Event_OOM:
		cntr, err := c.containerStore.Get(e.ID)
		if err != nil {
			glog.Errorf("Failed to get container %q: %v", e.ID, err)
		}
		err = cntr.Status.Update(func(status containerstore.Status) (containerstore.Status, error) {
			status.Reason = oomExitReason
			return status, nil
		})
		if err != nil {
			glog.Errorf("Failed to update container %q oom: %v", e.ID, err)
			return
		}
	}
}
