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

	"github.com/containerd/containerd/api/services/containers"
	"github.com/containerd/containerd/api/services/execution"
	"github.com/containerd/containerd/api/types/task"
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

	// Get container metadata from our container store.
	meta, err := c.containerStore.Get(r.GetContainerId())
	if err != nil {
		return nil, fmt.Errorf("an error occurred when try to find container %q: %v", r.GetContainerId(), err)
	}
	id := meta.ID

	if meta.State() != runtime.ContainerState_CONTAINER_RUNNING {
		return nil, fmt.Errorf("container %q is in %s state", id, criContainerStateToString(meta.State()))
	}

	// Get exec process spec.
	cntrResp, err := c.containerService.Get(ctx, &containers.GetContainerRequest{ID: id})
	if err != nil {
		return nil, fmt.Errorf("failed to get container %q from containerd: %v", id, err)
	}
	var spec runtimespec.Spec
	if err := json.Unmarshal(cntrResp.Container.Spec.Value, &spec); err != nil {
		return nil, fmt.Errorf("failed to unmarshal container spec: %v", err)
	}
	pspec := &spec.Process
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
	// TODO(random-liu): Handle this in event handler. Create an events client for
	// each exec introduces unnecessary overhead.
	cancellable, cancel := context.WithCancel(ctx)
	events, err := c.taskService.Events(cancellable, &execution.EventsRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to get containerd event: %v", err)
	}

	resp, err := c.taskService.Exec(ctx, &execution.ExecRequest{
		ContainerID: id,
		Terminal:    false,
		Stdout:      stdout,
		Stderr:      stderr,
		Spec: &prototypes.Any{
			TypeUrl: runtimespec.Version,
			Value:   rawSpec,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to exec in container %q: %v", id, err)
	}
	exitCode, err := waitContainerExec(cancel, events, id, resp.Pid, r.GetTimeout())
	if err != nil {
		return nil, fmt.Errorf("failed to wait for exec in container %q to finish: %v", id, err)
	}

	// TODO(random-liu): Make sure stdout/stderr are drained.
	return &runtime.ExecSyncResponse{
		Stdout:   stdoutBuf.Bytes(),
		Stderr:   stderrBuf.Bytes(),
		ExitCode: int32(exitCode),
	}, nil
}

// waitContainerExec waits for container exec to finish and returns the exit code.
func waitContainerExec(cancel context.CancelFunc, events execution.Tasks_EventsClient, id string,
	pid uint32, timeout int64) (uint32, error) {
	// TODO(random-liu): [P1] Support ExecSync timeout.
	// TODO(random-liu): Delete process after containerd upgrade.
	defer func() {
		// Stop events and drain the event channel. grpc-go#188
		cancel()
		for {
			_, err := events.Recv()
			if err != nil {
				break
			}
		}
	}()
	for {
		e, err := events.Recv()
		if err != nil {
			// Return non-zero exit code just in case.
			return unknownExitCode, err
		}
		if e.Type != task.Event_EXIT {
			continue
		}
		if e.ID == id && e.Pid == pid {
			return e.ExitStatus, nil
		}
	}
}
