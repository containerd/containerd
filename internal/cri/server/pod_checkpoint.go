/*
   Copyright The containerd Authors.

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
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/errdefs"
	"github.com/containerd/errdefs/pkg/errgrpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/containerd/containerd/v2/core/sandbox"
	containerstore "github.com/containerd/containerd/v2/internal/cri/store/container"
	sandboxstore "github.com/containerd/containerd/v2/internal/cri/store/sandbox"
)

type checkpointOutputDirectory struct {
	path string
	dir  *os.File
	info os.FileInfo
}

// CheckpointPod checkpoints all requested containers as one sandbox-wide
// consistency domain. The sandbox runtime, rather than the CRI adapter,
// implements freeze/capture/resume ordering.
func (c *criService) CheckpointPod(ctx context.Context, request *runtime.CheckpointPodRequest) (*runtime.CheckpointPodResponse, error) {
	if request == nil {
		return nil, status.Error(codes.InvalidArgument, "checkpoint request is required")
	}
	sandboxID := request.GetPodSandboxId()
	if sandboxID == "" {
		return nil, status.Error(codes.InvalidArgument, "pod sandbox id is required")
	}
	if len(request.GetContainerIds()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "at least one container id is required")
	}
	output, err := openCheckpointOutputPath(request.GetOutputPath())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	defer output.dir.Close()

	sb, unlock, err := c.lockSandboxOperationByID(ctx, sandboxID)
	if err != nil {
		return nil, toCRIError(fmt.Errorf("failed to find sandbox %q: %w", sandboxID, err))
	}
	defer unlock()
	if c.hasActiveSandboxExec(sb.ID) {
		return nil, status.Errorf(codes.FailedPrecondition, "sandbox %q has an active exec process", sb.ID)
	}
	if sb.Status.Get().State != sandboxstore.StateReady {
		return nil, status.Errorf(codes.FailedPrecondition, "sandbox %q is not ready", sandboxID)
	}

	// FIXME: This is a temporary check to avoid checkpointing pod user namespaces until we have a proper design for it.
	if userns := sb.Config.GetLinux().GetSecurityContext().GetNamespaceOptions().GetUsernsOptions(); userns != nil && userns.GetMode() == runtime.NamespaceMode_POD {
		return nil, status.Error(codes.InvalidArgument, "pod user namespaces are unsupported by pod checkpoint right now")
	}

	seenIDs := make(map[string]struct{}, len(request.GetContainerIds()))
	seenNames := make(map[string]struct{}, len(request.GetContainerIds()))
	tasks := make([]sandbox.CheckpointTask, 0, len(request.GetContainerIds()))
	for _, containerID := range request.GetContainerIds() {
		if containerID == "" {
			return nil, status.Error(codes.InvalidArgument, "container id must not be empty")
		}
		if _, ok := seenIDs[containerID]; ok {
			return nil, status.Errorf(codes.InvalidArgument, "duplicate container id %q", containerID)
		}
		seenIDs[containerID] = struct{}{}

		container, err := c.containerStore.Get(containerID)
		if err != nil {
			return nil, toCRIError(fmt.Errorf("failed to find container %q: %w", containerID, err))
		}
		if container.SandboxID != sb.ID {
			return nil, status.Errorf(codes.InvalidArgument, "container %q does not belong to sandbox %q", containerID, sb.ID)
		}
		if container.Status.Get().State() != runtime.ContainerState_CONTAINER_RUNNING {
			return nil, status.Errorf(codes.FailedPrecondition, "container %q is not running", containerID)
		}
		active, err := hasPersistedActiveExec(ctx, container)
		if err != nil {
			return nil, toCRIError(fmt.Errorf("failed to inspect exec processes for container %q: %w", containerID, err))
		}
		// FIXME: This is a temporary check to avoid checkpointing containers with active exec processes until we have a proper design for it.
		if active {
			return nil, status.Errorf(codes.FailedPrecondition, "container %q has an active exec process", containerID)
		}
		if err := validatePodCheckpointRestoreContainerConfig(container.Config); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "container %q: %v", containerID, err)
		}
		image, err := c.GetImage(container.ImageRef)
		if err != nil {
			return nil, toCRIError(fmt.Errorf("failed to inspect image for container %q: %w", containerID, err))
		}
		if err := validatePodCheckpointRestoreImage(image); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "container %q: %v", containerID, err)
		}
		name := container.Config.GetMetadata().GetName()
		if name == "" {
			return nil, status.Errorf(codes.InvalidArgument, "container %q has no Kubernetes container name", containerID)
		}
		if _, ok := seenNames[name]; ok {
			return nil, status.Errorf(codes.InvalidArgument, "duplicate container name %q", name)
		}
		seenNames[name] = struct{}{}
		tasks = append(tasks, sandbox.CheckpointTask{CheckpointKey: name, TaskID: container.ID})
	}
	for _, container := range c.containerStore.List() {
		if container.SandboxID != sb.ID || container.Status.Get().State() != runtime.ContainerState_CONTAINER_RUNNING {
			continue
		}
		if _, ok := seenIDs[container.ID]; !ok {
			return nil, status.Errorf(codes.FailedPrecondition, "checkpoint inventory omits running container %q", container.ID)
		}
	}
	checkpointService, ok := c.sandboxService.(checkpointRestoreSandboxService)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "sandbox service does not support checkpoint")
	}
	if err := output.validateIdentity(); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	err = checkpointService.CheckpointSandbox(ctx, sb.Sandboxer, sb.ID, sandbox.CheckpointOptions{
		OutputPath: request.GetOutputPath(),
		Tasks:      tasks,
		Options:    cloneStringMap(request.GetOptions()),
	})
	if err != nil {
		return nil, toCRIError(err)
	}
	if err := output.validateIdentity(); err != nil {
		return nil, status.Errorf(codes.Internal, "output path changed during checkpoint: %v", err)
	}
	return &runtime.CheckpointPodResponse{}, nil
}

func hasPersistedActiveExec(ctx context.Context, container containerstore.Container) (bool, error) {
	ids := container.Status.Get().ActiveExecIDs
	if len(ids) == 0 {
		return false, nil
	}
	task, err := container.Container.Task(ctx, nil)
	if err != nil {
		return false, err
	}
	stale := make(map[string]struct{})
	for _, id := range ids {
		process, err := task.LoadProcess(ctx, id, nil)
		if err != nil {
			if errdefs.IsNotFound(err) {
				stale[id] = struct{}{}
				continue
			}
			return false, err
		}
		state, err := process.Status(ctx)
		if err != nil {
			if errdefs.IsNotFound(err) {
				stale[id] = struct{}{}
				continue
			}
			return false, err
		}
		if state.Status != containerd.Stopped {
			return true, nil
		}
		stale[id] = struct{}{}
	}
	if len(stale) != 0 {
		if err := container.Status.UpdateSync(func(s containerstore.Status) (containerstore.Status, error) {
			kept := s.ActiveExecIDs[:0]
			for _, id := range s.ActiveExecIDs {
				if _, ok := stale[id]; !ok {
					kept = append(kept, id)
				}
			}
			s.ActiveExecIDs = kept
			return s, nil
		}); err != nil {
			return false, err
		}
	}
	return false, nil
}

func toCRIError(err error) error {
	mapped := errgrpc.ToGRPC(err)
	if status.Code(mapped) != codes.Unknown {
		return mapped
	}
	return status.Error(codes.Internal, err.Error())
}

func validateCheckpointOutputPath(path string) error {
	output, err := openCheckpointOutputPath(path)
	if err != nil {
		return err
	}
	return output.dir.Close()
}

func openCheckpointOutputPath(path string) (*checkpointOutputDirectory, error) {
	if path == "" {
		return nil, fmt.Errorf("output path is required")
	}
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("output path %q must be absolute", path)
	}
	dir, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open output path %q: %w", path, err)
	}
	output := &checkpointOutputDirectory{path: path, dir: dir}
	opened := false
	defer func() {
		if !opened {
			dir.Close()
		}
	}()
	output.info, err = dir.Stat()
	if err != nil {
		return nil, fmt.Errorf("failed to inspect output path %q: %w", path, err)
	}
	if !output.info.IsDir() {
		return nil, fmt.Errorf("output path %q must be a directory", path)
	}
	if err := output.validateIdentity(); err != nil {
		return nil, err
	}
	entries, readErr := dir.ReadDir(1)
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return nil, fmt.Errorf("failed to read output path %q: %w", path, readErr)
	}
	if len(entries) != 0 {
		return nil, fmt.Errorf("output path %q must be empty", path)
	}
	opened = true
	return output, nil
}

func (o *checkpointOutputDirectory) validateIdentity() error {
	info, err := os.Lstat(o.path)
	if err != nil {
		return fmt.Errorf("failed to inspect output path %q: %w", o.path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !os.SameFile(o.info, info) {
		return fmt.Errorf("output path %q must remain the same non-symlink directory", o.path)
	}
	return nil
}

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
