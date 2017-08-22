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
	"github.com/golang/protobuf/proto"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	"golang.org/x/net/context"
	"k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1/runtime"
)

// UpdateContainerResources updates ContainerConfig of the container.
func (c *criContainerdService) UpdateContainerResources(ctx context.Context, r *runtime.UpdateContainerResourcesRequest) (retRes *runtime.UpdateContainerResourcesResponse, retErr error) {
	cntr, err := c.containerStore.Get(r.GetContainerId())
	if err != nil {
		return nil, fmt.Errorf("failed to find container: %v", err)
	}
	task, err := cntr.Container.Task(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to find task: %v", err)
	}
	resources := toOCIResources(r.GetLinux())
	if err := task.Update(ctx, containerd.WithResources(resources)); err != nil {
		return nil, fmt.Errorf("failed to update resources: %v", err)
	}
	return &runtime.UpdateContainerResourcesResponse{}, nil
}

// toOCIResources converts CRI resource constraints to OCI.
func toOCIResources(r *runtime.LinuxContainerResources) *runtimespec.LinuxResources {
	return &runtimespec.LinuxResources{
		CPU: &runtimespec.LinuxCPU{
			Shares: proto.Uint64(uint64(r.GetCpuShares())),
			Quota:  proto.Int64(r.GetCpuQuota()),
			Period: proto.Uint64(uint64(r.GetCpuPeriod())),
			Cpus:   r.GetCpusetCpus(),
			Mems:   r.GetCpusetMems(),
		},
		Memory: &runtimespec.LinuxMemory{
			Limit: proto.Int64(r.GetMemoryLimitInBytes()),
		},
	}
}
