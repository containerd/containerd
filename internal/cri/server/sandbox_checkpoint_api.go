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
	"fmt"
	"path/filepath"
	"slices"

	"github.com/containerd/containerd/v2/core/sandbox"
	sandboxstore "github.com/containerd/containerd/v2/internal/cri/store/sandbox"
	"github.com/containerd/errdefs"
	"github.com/containerd/typeurl/v2"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// CheckpointPod coordinates CRI-owned Pod validation and delegates the
// implementation-specific checkpoint transaction to the sandbox controller.
func (c *criService) CheckpointPod(ctx context.Context, r *runtime.CheckpointPodRequest) (*runtime.CheckpointPodResponse, error) {
	if r == nil {
		return nil, fmt.Errorf("checkpoint request is required: %w", errdefs.ErrInvalidArgument)
	}
	if err := requirePodCheckpointDeadline(ctx, "pod checkpoint"); err != nil {
		return nil, err
	}
	if r.GetPodSandboxId() == "" {
		return nil, fmt.Errorf("pod sandbox ID is required: %w", errdefs.ErrInvalidArgument)
	}
	if err := c.checkPodCheckpointSupport(); err != nil {
		return nil, fmt.Errorf("pod checkpoint is unavailable: %w", err)
	}

	sb, err := c.sandboxStore.Get(r.GetPodSandboxId())
	if err != nil {
		return nil, fmt.Errorf("failed to find sandbox %q: %w", r.GetPodSandboxId(), err)
	}
	if state := sb.Status.Get().State; state != sandboxstore.StateReady {
		return nil, fmt.Errorf("pod sandbox %q must be running, found state %s: %w", sb.ID, state, errdefs.ErrFailedPrecondition)
	}
	controller, err := c.checkpointController(sb.Sandboxer, "checkpoint")
	if err != nil {
		return nil, err
	}

	opts, containerIDs, err := c.checkpointOptions(ctx, sb, r)
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

// checkpointOptions resolves the requested container IDs and supplies their CRI
// configuration and status. The controller validates the selected containers.
func (c *criService) checkpointOptions(ctx context.Context, sb sandboxstore.Sandbox, r *runtime.CheckpointPodRequest) (sandbox.CheckpointOptions, []string, error) {
	if r.GetOutputPath() == "" {
		return sandbox.CheckpointOptions{}, nil, fmt.Errorf("checkpoint output path is required: %w", errdefs.ErrInvalidArgument)
	}
	if !filepath.IsAbs(r.GetOutputPath()) {
		return sandbox.CheckpointOptions{}, nil, fmt.Errorf("checkpoint output path %q must be absolute: %w", r.GetOutputPath(), errdefs.ErrInvalidArgument)
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
	containerIDs := make([]string, 0, len(r.GetContainerIds()))
	for _, requestedID := range r.GetContainerIds() {
		cntr, err := c.containerStore.Get(requestedID)
		if err != nil {
			return sandbox.CheckpointOptions{}, nil, fmt.Errorf("failed to find checkpoint container %q: %w", requestedID, err)
		}
		// Truncated IDs can name the same container twice, which would make
		// the container reservation conflict with itself.
		if slices.Contains(containerIDs, cntr.ID) {
			return sandbox.CheckpointOptions{}, nil, fmt.Errorf("container %q is selected more than once: %w", cntr.ID, errdefs.ErrInvalidArgument)
		}
		config, err := typeurl.MarshalAny(cntr.Config)
		if err != nil {
			return sandbox.CheckpointOptions{}, nil, fmt.Errorf("failed to encode checkpoint container %q config: %w", cntr.ID, err)
		}
		// The pause controller verifies ImageRef against the restored
		// container, so record the image config digest rather than the
		// repository digest that ContainerStatus reports.
		criStatus, err := toCRIContainerStatus(ctx, cntr, cntr.Config.GetImage(), cntr.ImageRef, cntr.ImageRef)
		if err != nil {
			return sandbox.CheckpointOptions{}, nil, fmt.Errorf("failed to get checkpoint container %q status: %w", cntr.ID, err)
		}
		status, err := typeurl.MarshalAny(criStatus)
		if err != nil {
			return sandbox.CheckpointOptions{}, nil, fmt.Errorf("failed to encode checkpoint container %q status: %w", cntr.ID, err)
		}
		opts.Containers = append(opts.Containers, sandbox.CheckpointContainer{
			ID:     cntr.ID,
			Name:   cntr.Config.GetMetadata().GetName(),
			Config: config,
			Status: status,
		})
		containerIDs = append(containerIDs, cntr.ID)
	}
	return opts, containerIDs, nil
}

func (c *criService) checkPodCheckpointSupport() error {
	if !c.config.CheckpointRestoreEnabled() {
		return fmt.Errorf("checkpoint/restore support is disabled by configuration: %w", errdefs.ErrFailedPrecondition)
	}
	return nil
}

func requirePodCheckpointDeadline(ctx context.Context, operation string) error {
	if ctx == nil {
		return fmt.Errorf("%s requires a non-nil context with a finite deadline: %w", operation, errdefs.ErrInvalidArgument)
	}
	if _, ok := ctx.Deadline(); !ok {
		return fmt.Errorf("%s requires a finite context deadline: %w", operation, errdefs.ErrInvalidArgument)
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
			return nil, fmt.Errorf("checkpoint for container %q is already in progress: %w", containerID, errdefs.ErrAlreadyExists)
		}
		reserved = append(reserved, containerID)
	}
	return release, nil
}
