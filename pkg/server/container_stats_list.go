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

	"github.com/containerd/cgroups"
	tasks "github.com/containerd/containerd/api/services/tasks/v1"
	"github.com/containerd/containerd/api/types"
	"github.com/containerd/typeurl"
	"github.com/golang/glog"
	"golang.org/x/net/context"
	"k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1/runtime"
)

// ListContainerStats returns stats of all running containers.
func (c *criContainerdService) ListContainerStats(
	ctx context.Context,
	in *runtime.ListContainerStatsRequest,
) (*runtime.ListContainerStatsResponse, error) {
	request, candidateContainers, err := c.buildTaskMetricsRequest(in)
	if err != nil {
		return nil, fmt.Errorf("failed to build metrics request: %v", err)
	}
	resp, err := c.taskService.Metrics(ctx, &request)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch metrics for tasks: %v", err)
	}
	criStats, err := c.toCRIContainerStats(resp.Metrics, candidateContainers)
	if err != nil {
		return nil, fmt.Errorf("failed to convert to cri containerd stats format: %v", err)
	}
	return criStats, nil
}

func (c *criContainerdService) toCRIContainerStats(
	stats []*types.Metric,
	candidateContainers map[string]bool,
) (*runtime.ListContainerStatsResponse, error) {
	containerStats := new(runtime.ListContainerStatsResponse)
	for _, stat := range stats {
		var cs runtime.ContainerStats
		if err := c.getContainerMetrics(stat.ID, stat, &cs); err != nil {
			glog.Errorf("failed to decode container metrics: %v", err)
			continue
		}
		delete(candidateContainers, stat.ID)
		containerStats.Stats = append(containerStats.Stats, &cs)
	}
	// If there is a state where containers are dead at the time of query
	// but not removed, then check if the writeableLayer information
	// is present and attach the same.
	for id := range candidateContainers {
		var cs runtime.ContainerStats
		if err := c.getContainerMetrics(id, nil, &cs); err != nil {
			glog.Errorf("failed to decode container metrics: %v", err)
			continue
		}
		containerStats.Stats = append(containerStats.Stats, &cs)
	}
	return containerStats, nil
}

func (c *criContainerdService) getContainerMetrics(
	containerID string,
	stats *types.Metric,
	cs *runtime.ContainerStats,
) error {
	var usedBytes, inodesUsed uint64
	sn, err := c.snapshotStore.Get(containerID)
	// If snapshotstore doesn't have cached snapshot information
	// set WritableLayer usage to zero
	if err == nil {
		inodesUsed = sn.Size
		usedBytes = sn.Inodes
	}
	cs.WritableLayer = &runtime.FilesystemUsage{
		Timestamp: sn.Timestamp,
		StorageId: &runtime.StorageIdentifier{
			Uuid: c.imageFSUUID,
		},
		UsedBytes:  &runtime.UInt64Value{usedBytes},
		InodesUsed: &runtime.UInt64Value{inodesUsed},
	}

	// Get the container from store and extract the attributes
	cnt, err := c.containerStore.Get(containerID)
	if err != nil {
		return fmt.Errorf("failed to find container %q in container store: %v", containerID, err)
	}
	cs.Attributes = &runtime.ContainerAttributes{
		Id:          containerID,
		Metadata:    cnt.Config.GetMetadata(),
		Labels:      cnt.Config.GetLabels(),
		Annotations: cnt.Config.GetAnnotations(),
	}

	if stats != nil {
		s, err := typeurl.UnmarshalAny(stats.Data)
		if err != nil {
			return fmt.Errorf("failed to extract container metrics: %v", err)
		}
		metrics := s.(*cgroups.Metrics)
		cs.Cpu = &runtime.CpuUsage{
			Timestamp:            stats.Timestamp.UnixNano(),
			UsageCoreNanoSeconds: &runtime.UInt64Value{metrics.CPU.Usage.Total},
		}
		cs.Memory = &runtime.MemoryUsage{
			Timestamp:       stats.Timestamp.UnixNano(),
			WorkingSetBytes: &runtime.UInt64Value{metrics.Memory.Usage.Usage},
		}
	}

	return nil
}

// buildTaskMetricsRequest constructs a tasks.MetricsRequest based on
// the information in the stats request and the containerStore
func (c *criContainerdService) buildTaskMetricsRequest(
	r *runtime.ListContainerStatsRequest,
) (tasks.MetricsRequest, map[string]bool, error) {
	var req tasks.MetricsRequest
	if r.GetFilter == nil {
		return req, nil, nil
	}

	candidateContainers := make(map[string]bool)
	for _, c := range c.containerStore.List() {
		if r.Filter.GetId() != "" && c.ID != r.Filter.GetId() {
			continue
		}
		if r.Filter.GetPodSandboxId() != "" && c.SandboxID != r.Filter.GetPodSandboxId() {
			continue
		}
		if r.Filter.GetLabelSelector() != nil && !matchLabelSelector(r.Filter.GetLabelSelector(), c.Config.GetLabels()) {
			continue
		}
		candidateContainers[c.ID] = true
	}
	for id := range candidateContainers {
		req.Filters = append(req.Filters, "id=="+id)
	}
	return req, candidateContainers, nil
}

func matchLabelSelector(selector, labels map[string]string) bool {
	for k, v := range selector {
		if val, ok := labels[k]; ok {
			if v != val {
				return false
			}
		} else {
			return false
		}
	}
	return true
}
