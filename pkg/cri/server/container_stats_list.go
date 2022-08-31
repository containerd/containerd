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
	"time"

	"github.com/containerd/containerd/pkg/cri/store/stats"

	tasks "github.com/containerd/containerd/api/services/tasks/v1"
	"github.com/containerd/containerd/api/types"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	containerstore "github.com/containerd/containerd/pkg/cri/store/container"
)

// ListContainerStats returns stats of all running containers.
func (c *criService) ListContainerStats(
	ctx context.Context,
	in *runtime.ListContainerStatsRequest,
) (*runtime.ListContainerStatsResponse, error) {
	request, containers, err := c.buildTaskMetricsRequest(in)
	if err != nil {
		return nil, fmt.Errorf("failed to build metrics request: %w", err)
	}
	resp, err := c.client.TaskService().Metrics(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch metrics for tasks: %w", err)
	}
	criStats, err := c.toCRIContainerStats(resp.Metrics, containers)
	if err != nil {
		return nil, fmt.Errorf("failed to convert to cri containerd stats format: %w", err)
	}
	return criStats, nil
}

func (c *criService) toCRIContainerStats(
	stats []*types.Metric,
	containers []containerstore.Container,
) (*runtime.ListContainerStatsResponse, error) {
	statsMap := make(map[string]*types.Metric)
	for _, stat := range stats {
		statsMap[stat.ID] = stat
	}
	containerStats := new(runtime.ListContainerStatsResponse)
	for _, cntr := range containers {
		cs, err := c.containerMetrics(cntr.Metadata, statsMap[cntr.ID])
		if err != nil {
			return nil, fmt.Errorf("failed to decode container metrics for %q: %w", cntr.ID, err)
		}

		if cs.Cpu != nil && cs.Cpu.UsageCoreNanoSeconds != nil {
			// this is a calculated value and should be computed for all OSes
			nanoUsage, err := c.getUsageNanoCores(cntr.Metadata.ID, false, cs.Cpu.UsageCoreNanoSeconds.Value, time.Unix(0, cs.Cpu.Timestamp))
			if err != nil {
				return nil, fmt.Errorf("failed to get usage nano cores, containerID: %s: %w", cntr.Metadata.ID, err)
			}
			cs.Cpu.UsageNanoCores = &runtime.UInt64Value{Value: nanoUsage}
		}

		containerStats.Stats = append(containerStats.Stats, cs)
	}
	return containerStats, nil
}

func (c *criService) getUsageNanoCores(containerID string, isSandbox bool, currentUsageCoreNanoSeconds uint64, currentTimestamp time.Time) (uint64, error) {
	var oldStats *stats.ContainerStats

	if isSandbox {
		sandbox, err := c.sandboxStore.Get(containerID)
		if err != nil {
			return 0, fmt.Errorf("failed to get sandbox container: %s: %w", containerID, err)
		}
		oldStats = sandbox.Stats
	} else {
		container, err := c.containerStore.Get(containerID)
		if err != nil {
			return 0, fmt.Errorf("failed to get container ID: %s: %w", containerID, err)
		}
		oldStats = container.Stats
	}

	if oldStats == nil {
		newStats := &stats.ContainerStats{
			UsageCoreNanoSeconds: currentUsageCoreNanoSeconds,
			Timestamp:            currentTimestamp,
		}
		if isSandbox {
			err := c.sandboxStore.UpdateContainerStats(containerID, newStats)
			if err != nil {
				return 0, fmt.Errorf("failed to update sandbox stats container ID: %s: %w", containerID, err)
			}
		} else {
			err := c.containerStore.UpdateContainerStats(containerID, newStats)
			if err != nil {
				return 0, fmt.Errorf("failed to update container stats ID: %s: %w", containerID, err)
			}
		}
		return 0, nil
	}

	nanoSeconds := currentTimestamp.UnixNano() - oldStats.Timestamp.UnixNano()

	// zero or negative interval
	if nanoSeconds <= 0 {
		return 0, nil
	}

	newUsageNanoCores := uint64(float64(currentUsageCoreNanoSeconds-oldStats.UsageCoreNanoSeconds) /
		float64(nanoSeconds) * float64(time.Second/time.Nanosecond))

	newStats := &stats.ContainerStats{
		UsageCoreNanoSeconds: currentUsageCoreNanoSeconds,
		Timestamp:            currentTimestamp,
	}
	if isSandbox {
		err := c.sandboxStore.UpdateContainerStats(containerID, newStats)
		if err != nil {
			return 0, fmt.Errorf("failed to update sandbox container stats: %s: %w", containerID, err)
		}

	} else {
		err := c.containerStore.UpdateContainerStats(containerID, newStats)
		if err != nil {
			return 0, fmt.Errorf("failed to update container stats ID: %s: %w", containerID, err)
		}
	}

	return newUsageNanoCores, nil
}

func (c *criService) normalizeContainerStatsFilter(filter *runtime.ContainerStatsFilter) {
	if cntr, err := c.containerStore.Get(filter.GetId()); err == nil {
		filter.Id = cntr.ID
	}
	if sb, err := c.sandboxStore.Get(filter.GetPodSandboxId()); err == nil {
		filter.PodSandboxId = sb.ID
	}
}

// buildTaskMetricsRequest constructs a tasks.MetricsRequest based on
// the information in the stats request and the containerStore
func (c *criService) buildTaskMetricsRequest(
	r *runtime.ListContainerStatsRequest,
) (*tasks.MetricsRequest, []containerstore.Container, error) {
	req := &tasks.MetricsRequest{}
	if r.GetFilter() == nil {
		return req, c.containerStore.List(), nil
	}
	c.normalizeContainerStatsFilter(r.GetFilter())
	var containers []containerstore.Container
	for _, cntr := range c.containerStore.List() {
		if r.GetFilter().GetId() != "" && cntr.ID != r.GetFilter().GetId() {
			continue
		}
		if r.GetFilter().GetPodSandboxId() != "" && cntr.SandboxID != r.GetFilter().GetPodSandboxId() {
			continue
		}
		if r.GetFilter().GetLabelSelector() != nil &&
			!matchLabelSelector(r.GetFilter().GetLabelSelector(), cntr.Config.GetLabels()) {
			continue
		}
		containers = append(containers, cntr)
		req.Filters = append(req.Filters, "id=="+cntr.ID)
	}
	return req, containers, nil
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
