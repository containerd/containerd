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

	"github.com/containerd/containerd/v2/core/sandbox"
	containerstore "github.com/containerd/containerd/v2/internal/cri/store/container"
	sandboxstore "github.com/containerd/containerd/v2/internal/cri/store/sandbox"
	ctrdutil "github.com/containerd/containerd/v2/internal/cri/util"
	"github.com/containerd/errdefs"
	"github.com/containerd/typeurl/v2"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// CheckpointPod coordinates CRI-owned Pod validation and delegates the
// implementation-specific checkpoint transaction to the sandbox controller.
func (c *criService) CheckpointPod(ctx context.Context, r *runtime.CheckpointPodRequest) (*runtime.CheckpointPodResponse, error) {
	if r == nil {
		return nil, errors.New("checkpoint request is required")
	}
	if err := requirePodCheckpointDeadline(ctx, "pod checkpoint"); err != nil {
		return nil, err
	}
	if r.GetPodSandboxId() == "" {
		return nil, errors.New("pod sandbox ID is required")
	}
	if err := c.checkPodCheckpointSupport(); err != nil {
		return nil, fmt.Errorf("pod checkpoint is unavailable: %w", err)
	}

	sb, err := c.sandboxStore.Get(r.GetPodSandboxId())
	if err != nil {
		return nil, fmt.Errorf("failed to find sandbox %q: %w", r.GetPodSandboxId(), err)
	}
	if state := sb.Status.Get().State; state != sandboxstore.StateReady {
		return nil, fmt.Errorf("pod sandbox %q must be running, found state %s", sb.ID, state)
	}
	controller, err := c.checkpointController(sb.Sandboxer, "checkpoint")
	if err != nil {
		return nil, err
	}

	opts, containerIDs, err := c.checkpointOptions(sb, r)
	if err != nil {
		return nil, err
	}
	release, err := c.reserveContainerCheckpoints(containerIDs)
	if err != nil {
		return nil, err
	}
	defer release()

	if err := controller.Checkpoint(ctx, sb.ID, opts); err != nil {
		return nil, err
	}
	return &runtime.CheckpointPodResponse{}, nil
}

// RestorePod owns sandbox and container creation and asks the selected
// controller to prepare those resources from its checkpoint format.
func (c *criService) RestorePod(ctx context.Context, r *runtime.RestorePodRequest) (_ *runtime.RestorePodResponse, retErr error) {
	if r == nil {
		return nil, errors.New("restore request is required")
	}
	if err := requirePodCheckpointDeadline(ctx, "pod restore"); err != nil {
		return nil, err
	}
	if err := c.checkPodCheckpointSupport(); err != nil {
		return nil, fmt.Errorf("pod restore is unavailable: %w", err)
	}

	opts, err := restoreOptionsFromCRI(r)
	if err != nil {
		return nil, err
	}
	ociRuntime, err := c.config.GetSandboxRuntime(r.GetConfig(), r.GetRuntimeHandler())
	if err != nil {
		return nil, fmt.Errorf("failed to resolve restore runtime handler %q: %w", r.GetRuntimeHandler(), err)
	}
	controller, err := c.checkpointController(ociRuntime.Sandboxer, "restore")
	if err != nil {
		return nil, err
	}

	if err := c.ensureRestoreImages(ctx, r); err != nil {
		return nil, err
	}
	return restorePodResources(ctx, r, opts, controller, c)
}

// podRestoreOperations is the CRI-owned side of the restore transaction. It is
// deliberately kept on this side of CheckpointController so no CRI lifecycle
// callbacks or stores cross the controller boundary.
type podRestoreOperations interface {
	RunPodSandbox(context.Context, *runtime.RunPodSandboxRequest) (*runtime.RunPodSandboxResponse, error)
	CreateContainer(context.Context, *runtime.CreateContainerRequest) (*runtime.CreateContainerResponse, error)
	RemovePodSandbox(context.Context, *runtime.RemovePodSandboxRequest) (*runtime.RemovePodSandboxResponse, error)
	saveContainerTaskCheckpoint(string, string) error
	deleteTaskCheckpoint(context.Context, string) error
}

func restorePodResources(
	ctx context.Context,
	request *runtime.RestorePodRequest,
	opts sandbox.RestoreOptions,
	controller sandbox.CheckpointController,
	operations podRestoreOperations,
) (_ *runtime.RestorePodResponse, retErr error) {
	if len(request.GetContainerConfigs()) != len(opts.Containers) {
		return nil, fmt.Errorf("restore options contain %d containers, but request contains %d", len(opts.Containers), len(request.GetContainerConfigs()))
	}
	runResponse, err := operations.RunPodSandbox(ctx, &runtime.RunPodSandboxRequest{
		Config:         request.GetConfig(),
		RuntimeHandler: request.GetRuntimeHandler(),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create sandbox for restore: %w", err)
	}
	sandboxID := runResponse.GetPodSandboxId()
	if sandboxID == "" {
		return nil, errors.New("failed to create sandbox for restore: runtime returned an empty sandbox ID")
	}

	rollback := true
	var taskCheckpoints []string
	defer func() {
		if !rollback {
			return
		}
		cleanupCtx, cancel := ctrdutil.DeferContext()
		defer cancel()
		for _, image := range taskCheckpoints {
			if image == "" {
				continue
			}
			if err := operations.deleteTaskCheckpoint(cleanupCtx, image); err != nil && !errdefs.IsNotFound(err) {
				retErr = errors.Join(retErr, fmt.Errorf("failed to remove prepared task checkpoint image %q: %w", image, err))
			}
		}
		if _, err := operations.RemovePodSandbox(cleanupCtx, &runtime.RemovePodSandboxRequest{PodSandboxId: sandboxID}); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("failed to roll back restored sandbox %q: %w", sandboxID, err))
		}
	}()

	response := &runtime.RestorePodResponse{PodSandboxId: sandboxID}
	seenContainerIDs := make(map[string]struct{}, len(opts.Containers))
	for i, config := range request.GetContainerConfigs() {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("pod restore aborted before creating container %q: %w", opts.Containers[i].Name, err)
		}
		createResponse, err := operations.CreateContainer(ctx, &runtime.CreateContainerRequest{
			PodSandboxId:  sandboxID,
			Config:        config,
			SandboxConfig: request.GetConfig(),
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create restored container %q: %w", opts.Containers[i].Name, err)
		}
		containerID := createResponse.GetContainerId()
		if containerID == "" {
			return nil, fmt.Errorf("failed to create restored container %q: runtime returned an empty container ID", opts.Containers[i].Name)
		}
		if _, ok := seenContainerIDs[containerID]; ok {
			return nil, fmt.Errorf("failed to create restored container %q: runtime returned duplicate container ID %q", opts.Containers[i].Name, containerID)
		}
		seenContainerIDs[containerID] = struct{}{}
		opts.Containers[i].ID = containerID
		response.RestoredContainers = append(response.RestoredContainers, &runtime.RestoredContainer{
			Name:        opts.Containers[i].Name,
			ContainerId: containerID,
		})
	}

	result, err := controller.Restore(ctx, sandboxID, opts)
	if err != nil {
		return nil, err
	}
	for _, container := range result.RestoredContainers {
		if container.TaskCheckpointImage != "" {
			taskCheckpoints = append(taskCheckpoints, container.TaskCheckpointImage)
		}
	}
	prepared, err := validateRestoreResult(opts.Containers, result)
	if err != nil {
		return nil, err
	}
	for _, container := range opts.Containers {
		image := prepared[container.Name]
		if image == "" {
			continue
		}
		if err := operations.saveContainerTaskCheckpoint(container.ID, image); err != nil {
			return nil, fmt.Errorf("failed to save task checkpoint image for container %q: %w", container.ID, err)
		}
	}

	rollback = false
	return response, nil
}

func (c *criService) saveContainerTaskCheckpoint(containerID, image string) error {
	cntr, err := c.containerStore.Get(containerID)
	if err != nil {
		return err
	}
	return cntr.Status.UpdateSync(func(status containerstore.Status) (containerstore.Status, error) {
		status.TaskCheckpointImage = image
		return status, nil
	})
}

func (c *criService) deleteTaskCheckpoint(ctx context.Context, image string) error {
	return c.client.ImageService().Delete(ctx, image)
}

func (c *criService) checkpointController(sandboxer, operation string) (sandbox.CheckpointController, error) {
	controller, err := c.sandboxService.SandboxController(sandboxer)
	if err != nil {
		return nil, err
	}
	checkpointController, ok := controller.(sandbox.CheckpointController)
	if !ok {
		return nil, fmt.Errorf("sandbox controller %q does not support Pod %s: %w", sandboxer, operation, errdefs.ErrNotImplemented)
	}
	return checkpointController, nil
}

func (c *criService) checkpointOptions(sb sandboxstore.Sandbox, r *runtime.CheckpointPodRequest) (sandbox.CheckpointOptions, []string, error) {
	if r.GetOutputPath() == "" {
		return sandbox.CheckpointOptions{}, nil, errors.New("checkpoint output path is required")
	}
	if len(r.GetContainerIds()) == 0 {
		return sandbox.CheckpointOptions{}, nil, errors.New("at least one container ID is required for pod checkpoint")
	}
	sandboxConfig, err := typeurl.MarshalAny(sb.Config)
	if err != nil {
		return sandbox.CheckpointOptions{}, nil, fmt.Errorf("failed to encode sandbox checkpoint config: %w", err)
	}
	opts := sandbox.CheckpointOptions{
		OutputPath:    r.GetOutputPath(),
		SandboxConfig: sandboxConfig,
		Containers:    make([]sandbox.CheckpointContainer, 0, len(r.GetContainerIds())),
		Options:       r.GetOptions(),
	}
	seenRequested := make(map[string]struct{}, len(r.GetContainerIds()))
	seenContainers := make(map[string]struct{}, len(r.GetContainerIds()))
	seenNames := make(map[string]struct{}, len(r.GetContainerIds()))
	containerIDs := make([]string, 0, len(r.GetContainerIds()))
	for i, requestedID := range r.GetContainerIds() {
		if requestedID == "" {
			return sandbox.CheckpointOptions{}, nil, fmt.Errorf("container ID at index %d is empty", i)
		}
		if _, ok := seenRequested[requestedID]; ok {
			return sandbox.CheckpointOptions{}, nil, fmt.Errorf("container ID %q is duplicated in checkpoint request", requestedID)
		}
		seenRequested[requestedID] = struct{}{}

		cntr, err := c.containerStore.Get(requestedID)
		if err != nil {
			return sandbox.CheckpointOptions{}, nil, fmt.Errorf("failed to find checkpoint container %q: %w", requestedID, err)
		}
		if _, ok := seenContainers[cntr.ID]; ok {
			return sandbox.CheckpointOptions{}, nil, fmt.Errorf("container ID %q resolves to duplicate container %q", requestedID, cntr.ID)
		}
		seenContainers[cntr.ID] = struct{}{}
		if cntr.SandboxID != sb.ID {
			return sandbox.CheckpointOptions{}, nil, fmt.Errorf("checkpoint container %q belongs to sandbox %q, not requested sandbox %q", cntr.ID, cntr.SandboxID, sb.ID)
		}
		storedStatus := cntr.Status.Get()
		if state := storedStatus.State(); state != runtime.ContainerState_CONTAINER_RUNNING {
			return sandbox.CheckpointOptions{}, nil, fmt.Errorf("checkpoint container %q must be running, found state %s", cntr.ID, state)
		}
		name := cntr.Config.GetMetadata().GetName()
		if name == "" {
			return sandbox.CheckpointOptions{}, nil, fmt.Errorf("checkpoint container %q has no CRI metadata name", cntr.ID)
		}
		if _, ok := seenNames[name]; ok {
			return sandbox.CheckpointOptions{}, nil, fmt.Errorf("checkpoint container name %q is duplicated", name)
		}
		seenNames[name] = struct{}{}

		config, err := typeurl.MarshalAny(cntr.Config)
		if err != nil {
			return sandbox.CheckpointOptions{}, nil, fmt.Errorf("failed to encode checkpoint container %q config: %w", cntr.ID, err)
		}
		status, err := typeurl.MarshalAny(&runtime.ContainerStatus{
			Id:          cntr.ID,
			Metadata:    cntr.Config.GetMetadata(),
			State:       storedStatus.State(),
			CreatedAt:   storedStatus.CreatedAt,
			StartedAt:   storedStatus.StartedAt,
			FinishedAt:  storedStatus.FinishedAt,
			ExitCode:    storedStatus.ExitCode,
			Image:       cntr.Config.GetImage(),
			ImageRef:    cntr.ImageRef,
			Reason:      storedStatus.Reason,
			Message:     storedStatus.Message,
			Labels:      cntr.Config.GetLabels(),
			Annotations: cntr.Config.GetAnnotations(),
			Mounts:      cntr.Config.GetMounts(),
			LogPath:     cntr.LogPath,
			Resources:   storedStatus.Resources,
		})
		if err != nil {
			return sandbox.CheckpointOptions{}, nil, fmt.Errorf("failed to encode checkpoint container %q status: %w", cntr.ID, err)
		}
		opts.Containers = append(opts.Containers, sandbox.CheckpointContainer{
			ID:     cntr.ID,
			Name:   name,
			Config: config,
			Status: status,
		})
		containerIDs = append(containerIDs, cntr.ID)
	}
	return opts, containerIDs, nil
}

func restoreOptionsFromCRI(r *runtime.RestorePodRequest) (sandbox.RestoreOptions, error) {
	if r.GetCheckpointPath() == "" {
		return sandbox.RestoreOptions{}, errors.New("checkpoint path is required")
	}
	if r.GetConfig() == nil {
		return sandbox.RestoreOptions{}, errors.New("pod sandbox config is required for restore")
	}
	if len(r.GetContainerConfigs()) == 0 {
		return sandbox.RestoreOptions{}, errors.New("at least one container config is required for restore")
	}
	sandboxConfig, err := typeurl.MarshalAny(r.GetConfig())
	if err != nil {
		return sandbox.RestoreOptions{}, fmt.Errorf("failed to encode sandbox restore config: %w", err)
	}
	opts := sandbox.RestoreOptions{
		CheckpointPath: r.GetCheckpointPath(),
		Options:        r.GetOptions(),
		SandboxConfig:  sandboxConfig,
		Containers:     make([]sandbox.RestoreContainer, 0, len(r.GetContainerConfigs())),
	}
	seenNames := make(map[string]struct{}, len(r.GetContainerConfigs()))
	for i, config := range r.GetContainerConfigs() {
		if config == nil {
			return sandbox.RestoreOptions{}, fmt.Errorf("container config at index %d is required", i)
		}
		name := config.GetMetadata().GetName()
		if name == "" {
			return sandbox.RestoreOptions{}, fmt.Errorf("container config at index %d has no metadata name", i)
		}
		if _, ok := seenNames[name]; ok {
			return sandbox.RestoreOptions{}, fmt.Errorf("container config name %q is duplicated", name)
		}
		seenNames[name] = struct{}{}
		if config.GetImage().GetImage() == "" {
			return sandbox.RestoreOptions{}, fmt.Errorf("container config %q has no image", name)
		}
		encoded, err := typeurl.MarshalAny(config)
		if err != nil {
			return sandbox.RestoreOptions{}, fmt.Errorf("failed to encode sandbox restore container config at index %d: %w", i, err)
		}
		opts.Containers = append(opts.Containers, sandbox.RestoreContainer{
			Name:   name,
			Config: encoded,
		})
	}
	return opts, nil
}

func (c *criService) ensureRestoreImages(ctx context.Context, r *runtime.RestorePodRequest) error {
	seen := make(map[string]struct{}, len(r.GetContainerConfigs()))
	for _, config := range r.GetContainerConfigs() {
		image := config.GetImage().GetImage()
		if _, ok := seen[image]; ok {
			continue
		}
		seen[image] = struct{}{}
		if _, err := c.LocalResolve(image); err == nil {
			continue
		} else if !errdefs.IsNotFound(err) {
			return fmt.Errorf("failed to resolve restore image %q: %w", image, err)
		}
		if _, err := c.ImageService.PullImage(ctx, image, nil, r.GetConfig(), r.GetRuntimeHandler()); err != nil {
			return fmt.Errorf("failed to pull restore image %q: %w", image, err)
		}
	}
	return nil
}

func validateRestoreResult(containers []sandbox.RestoreContainer, result sandbox.RestoreResult) (map[string]string, error) {
	if len(result.RestoredContainers) != len(containers) {
		return nil, fmt.Errorf("sandbox controller prepared %d containers, expected %d", len(result.RestoredContainers), len(containers))
	}
	expected := make(map[string]struct{}, len(containers))
	for _, container := range containers {
		expected[container.Name] = struct{}{}
	}
	prepared := make(map[string]string, len(result.RestoredContainers))
	for i, container := range result.RestoredContainers {
		if container.Name == "" {
			return nil, fmt.Errorf("sandbox controller restore result at index %d has no container name", i)
		}
		if _, ok := expected[container.Name]; !ok {
			return nil, fmt.Errorf("sandbox controller prepared unexpected container %q", container.Name)
		}
		if _, ok := prepared[container.Name]; ok {
			return nil, fmt.Errorf("sandbox controller prepared container %q more than once", container.Name)
		}
		prepared[container.Name] = container.TaskCheckpointImage
	}
	return prepared, nil
}

func requirePodCheckpointDeadline(ctx context.Context, operation string) error {
	if ctx == nil {
		return fmt.Errorf("%s requires a non-nil context with a finite deadline", operation)
	}
	if _, ok := ctx.Deadline(); !ok {
		return fmt.Errorf("%s requires a finite context deadline", operation)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%s cannot start: %w", operation, err)
	}
	return nil
}

func (c *criService) reserveContainerCheckpoints(containerIDs []string) (func(), error) {
	reserved := make([]string, 0, len(containerIDs))
	release := func() {
		for i := len(reserved) - 1; i >= 0; i-- {
			c.containerCheckpointsInProgress.Delete(reserved[i])
		}
	}
	for _, containerID := range containerIDs {
		if _, loaded := c.containerCheckpointsInProgress.LoadOrStore(containerID, struct{}{}); loaded {
			release()
			return nil, fmt.Errorf("checkpoint for container %q is already in progress", containerID)
		}
		reserved = append(reserved, containerID)
	}
	return release, nil
}
