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
	"fmt"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/typeurl"
	"github.com/golang/glog"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/opencontainers/runtime-tools/generate"
	"golang.org/x/net/context"
	"k8s.io/apimachinery/pkg/conversion"
	"k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1/runtime"

	containerstore "github.com/kubernetes-incubator/cri-containerd/pkg/store/container"
)

// UpdateContainerResources updates ContainerConfig of the container.
func (c *criContainerdService) UpdateContainerResources(ctx context.Context, r *runtime.UpdateContainerResourcesRequest) (retRes *runtime.UpdateContainerResourcesResponse, retErr error) {
	container, err := c.containerStore.Get(r.GetContainerId())
	if err != nil {
		return nil, fmt.Errorf("failed to find container: %v", err)
	}
	// Update resources in status update transaction, so that:
	// 1) There won't be race condition with container start.
	// 2) There won't be concurrent resource update to the same container.
	if err := container.Status.Update(func(status containerstore.Status) (containerstore.Status, error) {
		return status, c.updateContainerResources(ctx, container, r.GetLinux(), status)
	}); err != nil {
		return nil, fmt.Errorf("failed to update resources: %v", err)
	}
	return &runtime.UpdateContainerResourcesResponse{}, nil
}

func (c *criContainerdService) updateContainerResources(ctx context.Context,
	cntr containerstore.Container,
	resources *runtime.LinuxContainerResources,
	status containerstore.Status) (retErr error) {
	id := cntr.ID
	// Do not update the container when there is a removal in progress.
	if status.Removing {
		return fmt.Errorf("container %q is in removing state", id)
	}

	// Update container spec. If the container is not started yet, updating
	// spec makes sure that the resource limits are correct when start;
	// if the container is already started, updating spec is still required,
	// the spec will become our source of truth for resource limits.
	oldSpec, err := cntr.Container.Get().Spec()
	if err != nil {
		return fmt.Errorf("failed to get container spec: %v", err)
	}
	newSpec, err := updateOCILinuxResource(oldSpec, resources)
	if err != nil {
		return fmt.Errorf("failed to update resource in spec: %v", err)
	}

	info := cntr.Container.Get().Info()
	any, err := typeurl.MarshalAny(newSpec)
	if err != nil {
		return fmt.Errorf("failed to marshal spec %+v: %v", newSpec, err)
	}
	info.Spec = any
	// TODO(random-liu): Add helper function in containerd to do the update.
	if _, err := c.client.ContainerService().Update(ctx, info, "spec"); err != nil {
		return fmt.Errorf("failed to update container spec: %v", err)
	}
	defer func() {
		if retErr != nil {
			// Reset spec on error.
			any, err := typeurl.MarshalAny(oldSpec)
			if err != nil {
				glog.Errorf("Failed to marshal spec %+v for container %q: %v", oldSpec, id, err)
				return
			}
			info.Spec = any
			if _, err := c.client.ContainerService().Update(ctx, info, "spec"); err != nil {
				glog.Errorf("Failed to recover spec %+v for container %q: %v", oldSpec, id, err)
			}
		}
	}()
	container, err := c.client.LoadContainer(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to load container: %v", err)
	}
	defer func() {
		if retErr == nil {
			// Update container client if no error is returned.
			// NOTE(random-liu): By updating container client, we'll be able
			// to get latest OCI spec from it, which includes the up-to-date
			// container resource limits. This will be useful after the debug
			// api is introduced.
			cntr.Container.Set(container)
		}
	}()

	// If container is not running, only update spec is enough, new resource
	// limit will be applied when container start.
	if status.State() != runtime.ContainerState_CONTAINER_RUNNING {
		return nil
	}

	task, err := container.Task(ctx, nil)
	if err != nil {
		if errdefs.IsNotFound(err) {
			// Task exited already.
			return nil
		}
		return fmt.Errorf("failed to get task: %v", err)
	}
	// newSpec.Linux won't be nil
	if err := task.Update(ctx, containerd.WithResources(newSpec.Linux.Resources)); err != nil {
		if errdefs.IsNotFound(err) {
			// Task exited already.
			return nil
		}
		return fmt.Errorf("failed to update resources: %v", err)
	}
	return nil
}

// updateOCILinuxResource updates container resource limit.
func updateOCILinuxResource(spec *runtimespec.Spec, new *runtime.LinuxContainerResources) (*runtimespec.Spec, error) {
	// Copy to make sure old spec is not changed.
	cloned, err := conversion.NewCloner().DeepCopy(spec)
	if err != nil {
		return nil, fmt.Errorf("failed to deep copy: %v", err)
	}
	g := generate.NewFromSpec(cloned.(*runtimespec.Spec))

	if new.GetCpuPeriod() != 0 {
		g.SetLinuxResourcesCPUPeriod(uint64(new.GetCpuPeriod()))
	}
	if new.GetCpuQuota() != 0 {
		g.SetLinuxResourcesCPUQuota(new.GetCpuQuota())
	}
	if new.GetCpuShares() != 0 {
		g.SetLinuxResourcesCPUShares(uint64(new.GetCpuShares()))
	}
	if new.GetMemoryLimitInBytes() != 0 {
		g.SetLinuxResourcesMemoryLimit(new.GetMemoryLimitInBytes())
	}
	// OOMScore is not updatable.
	if new.GetCpusetCpus() != "" {
		g.SetLinuxResourcesCPUCpus(new.GetCpusetCpus())
	}
	if new.GetCpusetMems() != "" {
		g.SetLinuxResourcesCPUMems(new.GetCpusetMems())
	}

	return g.Spec(), nil
}
