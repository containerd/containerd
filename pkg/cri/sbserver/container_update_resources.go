//go:build !darwin && !freebsd

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

package sbserver

import (
	"context"
	gocontext "context"
	"fmt"

	"github.com/containerd/typeurl/v2"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/log"

	containerstore "github.com/containerd/containerd/pkg/cri/store/container"
	ctrdutil "github.com/containerd/containerd/pkg/cri/util"
)

// UpdateContainerResources updates ContainerConfig of the container.
func (c *criService) UpdateContainerResources(ctx context.Context, r *runtime.UpdateContainerResourcesRequest) (retRes *runtime.UpdateContainerResourcesResponse, retErr error) {
	container, err := c.containerStore.Get(r.GetContainerId())
	if err != nil {
		return nil, fmt.Errorf("failed to find container: %w", err)
	}

	sandbox, err := c.sandboxStore.Get(container.SandboxID)
	if err != nil {
		return nil, err
	}

	resources := r.GetLinux()
	updated, err := c.nri.UpdateContainerResources(ctx, &sandbox, &container, resources)
	if err != nil {
		return nil, fmt.Errorf("NRI container update failed: %w", err)
	}
	if updated != nil {
		*resources = *updated
	}

	// Update resources in status update transaction, so that:
	// 1) There won't be race condition with container start.
	// 2) There won't be concurrent resource update to the same container.
	if err := container.Status.UpdateSync(func(status containerstore.Status) (containerstore.Status, error) {
		return c.updateContainerResources(ctx, container, r, status)
	}); err != nil {
		return nil, fmt.Errorf("failed to update resources: %w", err)
	}

	err = c.nri.PostUpdateContainerResources(ctx, &sandbox, &container)
	if err != nil {
		log.G(ctx).WithError(err).Errorf("NRI post-update notification failed")
	}

	return &runtime.UpdateContainerResourcesResponse{}, nil
}

func (c *criService) updateContainerResources(ctx context.Context,
	cntr containerstore.Container,
	r *runtime.UpdateContainerResourcesRequest,
	status containerstore.Status) (newStatus containerstore.Status, retErr error) {

	newStatus = status
	id := cntr.ID
	// Do not update the container when there is a removal in progress.
	if status.Removing {
		return newStatus, fmt.Errorf("container %q is in removing state", id)
	}

	// Update container spec. If the container is not started yet, updating
	// spec makes sure that the resource limits are correct when start;
	// if the container is already started, updating spec is still required,
	// the spec will become our source of truth for resource limits.
	oldSpec, err := cntr.Container.Spec(ctx)
	if err != nil {
		return newStatus, fmt.Errorf("failed to get container spec: %w", err)
	}
	newSpec, err := updateOCIResource(ctx, oldSpec, r, c.config)
	if err != nil {
		return newStatus, fmt.Errorf("failed to update resource in spec: %w", err)
	}

	if err := updateContainerSpec(ctx, cntr.Container, newSpec); err != nil {
		return newStatus, err
	}
	defer func() {
		if retErr != nil {
			deferCtx, deferCancel := ctrdutil.DeferContext()
			defer deferCancel()
			// Reset spec on error.
			if err := updateContainerSpec(deferCtx, cntr.Container, oldSpec); err != nil {
				log.G(ctx).WithError(err).Errorf("Failed to update spec %+v for container %q", oldSpec, id)
			}
		} else {
			// Update container status only when the spec is updated
			newStatus = copyResourcesToStatus(newSpec, status)
		}
	}()

	// If container is not running, only update spec is enough, new resource
	// limit will be applied when container start.
	if status.State() != runtime.ContainerState_CONTAINER_RUNNING {
		return newStatus, nil
	}

	task, err := cntr.Container.Task(ctx, nil)
	if err != nil {
		if errdefs.IsNotFound(err) {
			// Task exited already.
			return newStatus, nil
		}
		return newStatus, fmt.Errorf("failed to get task: %w", err)
	}
	// newSpec.Linux / newSpec.Windows won't be nil
	if err := task.Update(ctx, containerd.WithResources(getResources(newSpec))); err != nil {
		if errdefs.IsNotFound(err) {
			// Task exited already.
			return newStatus, nil
		}
		return newStatus, fmt.Errorf("failed to update resources: %w", err)
	}
	return newStatus, nil
}

// updateContainerSpec updates container spec.
func updateContainerSpec(ctx context.Context, cntr containerd.Container, spec *runtimespec.Spec) error {
	any, err := typeurl.MarshalAny(spec)
	if err != nil {
		return fmt.Errorf("failed to marshal spec %+v: %w", spec, err)
	}
	if err := cntr.Update(ctx, func(ctx gocontext.Context, client *containerd.Client, c *containers.Container) error {
		c.Spec = any
		return nil
	}); err != nil {
		return fmt.Errorf("failed to update container spec: %w", err)
	}
	return nil
}
