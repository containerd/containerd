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
	"github.com/golang/glog"
	"golang.org/x/net/context"

	"github.com/containerd/containerd/api/services/execution"
	"github.com/containerd/containerd/api/types/container"

	"github.com/kubernetes-incubator/cri-containerd/pkg/metadata"
)

// startEventMonitor starts an event monitor which monitors and handles all
// container events.
// TODO(random-liu): [P1] Figure out:
// 1) Is it possible to drop event during containerd is running?
// 2) How to deal with containerd down? We should restart event monitor, and
// we should recover all container state.
func (c *criContainerdService) startEventMonitor() error {
	events, err := c.containerService.Events(context.Background(), &execution.EventsRequest{})
	if err != nil {
		return err
	}
	go func() {
		for {
			c.handleEventStream(events)
		}
	}()
	return nil
}

// handleEventStream receives an event from containerd and handles the event.
func (c *criContainerdService) handleEventStream(events execution.ContainerService_EventsClient) {
	// TODO(random-liu): [P1] Should backoff on this error, or else this will
	// cause a busy loop.
	e, err := events.Recv()
	if err != nil {
		glog.Errorf("Failed to receive event: %v", err)
		return
	}
	glog.V(2).Infof("Received container event: %+v", e)
	c.handleEvent(e)
	return
}

// handleEvent handles a containerd event.
func (c *criContainerdService) handleEvent(e *container.Event) {
	switch e.Type {
	// If containerd-shim exits unexpectedly, there will be no corresponding event.
	// However, containerd could not retrieve container state in that case, so it's
	// fine to leave out that case for now.
	// TODO(random-liu): [P2] Handle containerd-shim exit.
	case container.Event_EXIT:
		meta, err := c.containerStore.Get(e.ID)
		if err != nil {
			glog.Errorf("Failed to get container %q metadata: %v", e.ID, err)
			return
		}
		if e.Pid != meta.Pid {
			// Non-init process died, ignore the event.
			return
		}
		// Delete the container from containerd.
		_, err = c.containerService.Delete(context.Background(), &execution.DeleteRequest{ID: e.ID})
		if err != nil && !isContainerdContainerNotExistError(err) {
			// TODO(random-liu): [P0] Enqueue the event and retry.
			glog.Errorf("Failed to delete container %q: %v", e.ID, err)
			return
		}
		err = c.containerStore.Update(e.ID, func(meta metadata.ContainerMetadata) (metadata.ContainerMetadata, error) {
			// If FinishedAt has been set (e.g. with start failure), keep as
			// it is.
			if meta.FinishedAt != 0 {
				return meta, nil
			}
			meta.Pid = 0
			meta.FinishedAt = e.ExitedAt.UnixNano()
			meta.ExitCode = int32(e.ExitStatus)
			return meta, nil
		})
		if err != nil {
			glog.Errorf("Failed to update container %q state: %v", e.ID, err)
			// TODO(random-liu): [P0] Enqueue the event and retry.
			return
		}
	case container.Event_OOM:
		// TODO(random-liu): [P1] Handle OOM event.
	}
}
