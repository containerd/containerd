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
	"bytes"
	"fmt"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/api/services/events/v1"
	"github.com/containerd/containerd/typeurl"
	"github.com/golang/glog"
	"golang.org/x/net/context"
	"k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1/runtime"
)

// ExecSync executes a command in the container, and returns the stdout output.
// If command exits with a non-zero exit code, an error is returned.
func (c *criContainerdService) ExecSync(ctx context.Context, r *runtime.ExecSyncRequest) (retRes *runtime.ExecSyncResponse, retErr error) {
	glog.V(2).Infof("ExecSync for %q with command %+v and timeout %d (s)", r.GetContainerId(), r.GetCmd(), r.GetTimeout())
	defer func() {
		if retErr == nil {
			glog.V(2).Infof("ExecSync for %q returns with exit code %d", r.GetContainerId(), retRes.GetExitCode())
			glog.V(4).Infof("ExecSync for %q outputs - stdout: %q, stderr: %q", r.GetContainerId(),
				retRes.GetStdout(), retRes.GetStderr())
		}
	}()

	// Get container from our container store.
	cntr, err := c.containerStore.Get(r.GetContainerId())
	if err != nil {
		return nil, fmt.Errorf("failed to find container in store: %v", err)
	}
	id := cntr.ID

	state := cntr.Status.Get().State()
	if state != runtime.ContainerState_CONTAINER_RUNNING {
		return nil, fmt.Errorf("container is in %s state", criContainerStateToString(state))
	}

	// TODO(random-liu): Store container client in container store.
	container, err := c.client.LoadContainer(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to load container: %v", err)
	}
	spec, err := container.Spec()
	if err != nil {
		return nil, fmt.Errorf("failed to get container spec: %v", err)
	}
	task, err := container.Task(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to load task: %v", err)
	}

	pspec := spec.Process
	pspec.Args = r.GetCmd()

	execID := generateID()
	stdinBuf, stdoutBuf, stderrBuf := new(bytes.Buffer), new(bytes.Buffer), new(bytes.Buffer)
	io := containerd.NewIOWithTerminal(stdinBuf, stdoutBuf, stderrBuf, pspec.Terminal)
	process, err := task.Exec(ctx, execID, pspec, io)
	if err != nil {
		return nil, fmt.Errorf("failed to create exec %q: %v", execID, err)
	}
	defer func() {
		if _, err := process.Delete(ctx); err != nil {
			glog.Errorf("Failed to delete exec process %q for container %q: %v", execID, id, err)
		}
	}()

	// Get containerd event client first, so that we won't miss any events.
	// TODO(random-liu): Add filter to only subscribe events of the exec process.
	// TODO(random-liu): Use `Wait` after is fixed. (containerd#1279, containerd#1287)
	cancellable, cancel := context.WithCancel(ctx)
	eventstream, err := c.eventService.Subscribe(cancellable, &events.SubscribeRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to subscribe event stream: %v", err)
	}
	defer cancel()

	if err := process.Start(ctx); err != nil {
		return nil, fmt.Errorf("failed to start exec %q: %v", execID, err)
	}

	exitCode, err := c.waitContainerExec(eventstream, id, execID)
	if err != nil {
		return nil, fmt.Errorf("failed to wait for exec in container %q to finish: %v", id, err)
	}
	// TODO(random-liu): [P1] Deal with timeout, kill and wait again on timeout.

	// Wait for the io to be drained.
	process.IO().Wait()

	return &runtime.ExecSyncResponse{
		Stdout:   stdoutBuf.Bytes(),
		Stderr:   stderrBuf.Bytes(),
		ExitCode: int32(exitCode),
	}, nil
}

// waitContainerExec waits for container exec to finish and returns the exit code.
func (c *criContainerdService) waitContainerExec(eventstream events.Events_SubscribeClient, id string,
	execID string) (uint32, error) {
	for {
		evt, err := eventstream.Recv()
		if err != nil {
			// Return non-zero exit code just in case.
			return unknownExitCode, err
		}
		// Continue until the event received is of type task exit.
		if !typeurl.Is(evt.Event, &events.TaskExit{}) {
			continue
		}
		any, err := typeurl.UnmarshalAny(evt.Event)
		if err != nil {
			return unknownExitCode, err
		}
		e := any.(*events.TaskExit)
		if e.ContainerID == id && e.ID == execID {
			return e.ExitStatus, nil
		}
	}
}
