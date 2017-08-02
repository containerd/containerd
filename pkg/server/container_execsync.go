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
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"

	"github.com/containerd/containerd/api/services/events/v1"
	"github.com/containerd/containerd/api/services/tasks/v1"
	"github.com/containerd/containerd/typeurl"
	prototypes "github.com/gogo/protobuf/types"
	"github.com/golang/glog"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
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
		return nil, fmt.Errorf("an error occurred when try to find container %q: %v", r.GetContainerId(), err)
	}
	id := cntr.ID

	state := cntr.Status.Get().State()
	if state != runtime.ContainerState_CONTAINER_RUNNING {
		return nil, fmt.Errorf("container %q is in %s state", id, criContainerStateToString(state))
	}

	// Get exec process spec.
	container, err := c.containerService.Get(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to get container %q from containerd: %v", id, err)
	}
	var spec runtimespec.Spec
	if err := json.Unmarshal(container.Spec.Value, &spec); err != nil {
		return nil, fmt.Errorf("failed to unmarshal container spec: %v", err)
	}
	pspec := spec.Process
	pspec.Args = r.GetCmd()
	rawSpec, err := json.Marshal(pspec)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal oci process spec %+v: %v", pspec, err)
	}

	// TODO(random-liu): Replace the following logic with containerd client and add unit test.
	// Prepare streaming pipes.
	execDir, err := ioutil.TempDir(getContainerRootDir(c.rootDir, id), "exec")
	if err != nil {
		return nil, fmt.Errorf("failed to create exec streaming directory: %v", err)
	}
	defer func() {
		if err = c.os.RemoveAll(execDir); err != nil {
			glog.Errorf("Failed to remove exec streaming directory %q: %v", execDir, err)
		}
	}()
	_, stdout, stderr := getStreamingPipes(execDir)
	_, stdoutPipe, stderrPipe, err := c.prepareStreamingPipes(ctx, "", stdout, stderr)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare streaming pipes: %v", err)
	}
	defer stdoutPipe.Close()
	defer stderrPipe.Close()

	// Start redirecting exec output.
	stdoutBuf, stderrBuf := new(bytes.Buffer), new(bytes.Buffer)
	go io.Copy(stdoutBuf, stdoutPipe) // nolint: errcheck
	go io.Copy(stderrBuf, stderrPipe) // nolint: errcheck

	// Get containerd event client first, so that we won't miss any events.
	// TODO(random-liu): Add filter to only subscribe events of the exec process.
	cancellable, cancel := context.WithCancel(ctx)
	eventstream, err := c.eventService.Subscribe(cancellable, &events.SubscribeRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to get containerd event: %v", err)
	}
	defer cancel()

	execID := generateID()
	_, err = c.taskService.Exec(ctx, &tasks.ExecProcessRequest{
		ContainerID: id,
		Terminal:    false,
		Stdout:      stdout,
		Stderr:      stderr,
		Spec: &prototypes.Any{
			TypeUrl: runtimespec.Version,
			Value:   rawSpec,
		},
		ExecID: execID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to exec in container %q: %v", id, err)
	}
	exitCode, err := c.waitContainerExec(eventstream, id, execID)
	if err != nil {
		return nil, fmt.Errorf("failed to wait for exec in container %q to finish: %v", id, err)
	}
	if _, err := c.taskService.DeleteProcess(ctx, &tasks.DeleteProcessRequest{
		ContainerID: id,
		ExecID:      execID,
	}); err != nil && !isContainerdGRPCNotFoundError(err) {
		return nil, fmt.Errorf("failed to delete exec %q in container %q: %v", execID, id, err)
	}
	// TODO(random-liu): [P1] Deal with timeout, kill and wait again on timeout.

	// TODO(random-liu): Make sure stdout/stderr are drained.
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
