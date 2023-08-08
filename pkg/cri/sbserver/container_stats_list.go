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
	"errors"
	"fmt"
	"reflect"
	"time"

	wstats "github.com/Microsoft/hcsshim/cmd/containerd-shim-runhcs-v1/stats"
	cg1 "github.com/containerd/cgroups/v3/cgroup1/stats"
	cg2 "github.com/containerd/cgroups/v3/cgroup2/stats"
	"github.com/containerd/containerd/api/services/tasks/v1"
	"github.com/containerd/containerd/api/types"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/pkg/cri/store/stats"
	"github.com/containerd/containerd/protobuf"
	"github.com/containerd/typeurl/v2"
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
	criStats, err := c.toCRIContainerStats(ctx, resp.Metrics, containers)
	if err != nil {
		return nil, fmt.Errorf("failed to convert to cri containerd stats format: %w", err)
	}
	return criStats, nil
}

type metricsHandler func(containerstore.Metadata, *types.Metric) (*runtime.ContainerStats, error)

// Returns a function to be used for transforming container metrics into the right format.
// Uses the platform the given sandbox advertises to implement its logic. If the platform is
// unsupported for metrics this will return a wrapped [errdefs.ErrNotImplemented].
func (c *criService) getMetricsHandler(ctx context.Context, sandboxID string) (metricsHandler, error) {
	sandbox, err := c.sandboxStore.Get(sandboxID)
	if err != nil {
		return nil, fmt.Errorf("failed to find sandbox id %q: %w", sandboxID, err)
	}
	controller, err := c.getSandboxController(sandbox.Config, sandbox.RuntimeHandler)
	if err != nil {
		return nil, fmt.Errorf("failed to get sandbox controller: %w", err)
	}
	// Grab the platform that this containers sandbox advertises. Reason being, even if
	// the host may be {insert platform}, if it virtualizes or emulates a different platform
	// it will return stats in that format, and we need to handle the conversion logic based
	// off of this info.
	p, err := controller.Platform(ctx, sandboxID)
	if err != nil {
		return nil, err
	}

	switch p.OS {
	case "windows":
		return c.windowsContainerMetrics, nil
	case "linux":
		return c.linuxContainerMetrics, nil
	default:
		return nil, fmt.Errorf("container metrics for platform %+v: %w", p, errdefs.ErrNotImplemented)
	}
}

func (c *criService) toCRIContainerStats(
	ctx context.Context,
	stats []*types.Metric,
	containers []containerstore.Container,
) (*runtime.ListContainerStatsResponse, error) {
	statsMap := make(map[string]*types.Metric)
	for _, stat := range stats {
		statsMap[stat.ID] = stat
	}
	containerStats := new(runtime.ListContainerStatsResponse)

	// Unfortunately if no filter was passed we're asking for every containers stats which
	// generally belong to multiple different pods, who all might have different platforms.
	// To avoid recalculating the right metricsHandler to invoke, if we've already calculated
	// the platform and handler for a given sandbox just pull it from our map here.
	var (
		err     error
		handler metricsHandler
	)
	sandboxToMetricsHandler := make(map[string]metricsHandler)
	for _, cntr := range containers {
		h, ok := sandboxToMetricsHandler[cntr.SandboxID]
		if !ok {
			handler, err = c.getMetricsHandler(ctx, cntr.SandboxID)
			if err != nil {
				// If the sandbox is not found, it may have been removed. we need to check container whether it is still exist
				if errdefs.IsNotFound(err) {
					_, err = c.containerStore.Get(cntr.ID)
					if err != nil && errdefs.IsNotFound(err) {
						log.G(ctx).Warnf("container %q is not found, skip it", cntr.ID)
						continue
					}
				}
				return nil, fmt.Errorf("failed to get metrics handler for container %q: %w", cntr.ID, err)
			}
			sandboxToMetricsHandler[cntr.SandboxID] = handler
		} else {
			handler = h
		}

		cs, err := handler(cntr.Metadata, statsMap[cntr.ID])
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

func (c *criService) windowsContainerMetrics(
	meta containerstore.Metadata,
	stats *types.Metric,
) (*runtime.ContainerStats, error) {
	var cs runtime.ContainerStats
	var usedBytes, inodesUsed uint64
	sn, err := c.GetSnapshot(meta.ID)
	// If snapshotstore doesn't have cached snapshot information
	// set WritableLayer usage to zero
	if err == nil {
		usedBytes = sn.Size
		inodesUsed = sn.Inodes
	}
	cs.WritableLayer = &runtime.FilesystemUsage{
		Timestamp: sn.Timestamp,
		FsId: &runtime.FilesystemIdentifier{
			Mountpoint: c.imageFSPath,
		},
		UsedBytes:  &runtime.UInt64Value{Value: usedBytes},
		InodesUsed: &runtime.UInt64Value{Value: inodesUsed},
	}
	cs.Attributes = &runtime.ContainerAttributes{
		Id:          meta.ID,
		Metadata:    meta.Config.GetMetadata(),
		Labels:      meta.Config.GetLabels(),
		Annotations: meta.Config.GetAnnotations(),
	}

	if stats != nil {
		s, err := typeurl.UnmarshalAny(stats.Data)
		if err != nil {
			return nil, fmt.Errorf("failed to extract container metrics: %w", err)
		}
		wstats := s.(*wstats.Statistics).GetWindows()
		if wstats == nil {
			return nil, errors.New("windows stats is empty")
		}
		if wstats.Processor != nil {
			cs.Cpu = &runtime.CpuUsage{
				Timestamp:            (protobuf.FromTimestamp(wstats.Timestamp)).UnixNano(),
				UsageCoreNanoSeconds: &runtime.UInt64Value{Value: wstats.Processor.TotalRuntimeNS},
			}
		}
		if wstats.Memory != nil {
			cs.Memory = &runtime.MemoryUsage{
				Timestamp: (protobuf.FromTimestamp(wstats.Timestamp)).UnixNano(),
				WorkingSetBytes: &runtime.UInt64Value{
					Value: wstats.Memory.MemoryUsagePrivateWorkingSetBytes,
				},
			}
		}
	}
	return &cs, nil
}

func (c *criService) linuxContainerMetrics(
	meta containerstore.Metadata,
	stats *types.Metric,
) (*runtime.ContainerStats, error) {
	var cs runtime.ContainerStats
	var usedBytes, inodesUsed uint64
	sn, err := c.GetSnapshot(meta.ID)
	// If snapshotstore doesn't have cached snapshot information
	// set WritableLayer usage to zero
	if err == nil {
		usedBytes = sn.Size
		inodesUsed = sn.Inodes
	}
	cs.WritableLayer = &runtime.FilesystemUsage{
		Timestamp: sn.Timestamp,
		FsId: &runtime.FilesystemIdentifier{
			Mountpoint: c.imageFSPath,
		},
		UsedBytes:  &runtime.UInt64Value{Value: usedBytes},
		InodesUsed: &runtime.UInt64Value{Value: inodesUsed},
	}
	cs.Attributes = &runtime.ContainerAttributes{
		Id:          meta.ID,
		Metadata:    meta.Config.GetMetadata(),
		Labels:      meta.Config.GetLabels(),
		Annotations: meta.Config.GetAnnotations(),
	}

	if stats != nil {
		var data interface{}
		switch {
		case typeurl.Is(stats.Data, (*cg1.Metrics)(nil)):
			data = &cg1.Metrics{}
		case typeurl.Is(stats.Data, (*cg2.Metrics)(nil)):
			data = &cg2.Metrics{}
		case typeurl.Is(stats.Data, (*wstats.Statistics)(nil)):
			data = &wstats.Statistics{}
		default:
			return nil, errors.New("cannot convert metric data to cgroups.Metrics or windows.Statistics")
		}

		if err := typeurl.UnmarshalTo(stats.Data, data); err != nil {
			return nil, fmt.Errorf("failed to extract container metrics: %w", err)
		}

		cpuStats, err := c.cpuContainerStats(meta.ID, false /* isSandbox */, data, protobuf.FromTimestamp(stats.Timestamp))
		if err != nil {
			return nil, fmt.Errorf("failed to obtain cpu stats: %w", err)
		}
		cs.Cpu = cpuStats

		memoryStats, err := c.memoryContainerStats(meta.ID, data, protobuf.FromTimestamp(stats.Timestamp))
		if err != nil {
			return nil, fmt.Errorf("failed to obtain memory stats: %w", err)
		}
		cs.Memory = memoryStats
	}

	return &cs, nil
}

// getWorkingSet calculates workingset memory from cgroup memory stats.
// The caller should make sure memory is not nil.
// workingset = usage - total_inactive_file
func getWorkingSet(memory *cg1.MemoryStat) uint64 {
	if memory.Usage == nil {
		return 0
	}
	var workingSet uint64
	if memory.TotalInactiveFile < memory.Usage.Usage {
		workingSet = memory.Usage.Usage - memory.TotalInactiveFile
	}
	return workingSet
}

// getWorkingSetV2 calculates workingset memory from cgroupv2 memory stats.
// The caller should make sure memory is not nil.
// workingset = usage - inactive_file
func getWorkingSetV2(memory *cg2.MemoryStat) uint64 {
	var workingSet uint64
	if memory.InactiveFile < memory.Usage {
		workingSet = memory.Usage - memory.InactiveFile
	}
	return workingSet
}

func isMemoryUnlimited(v uint64) bool {
	// Size after which we consider memory to be "unlimited". This is not
	// MaxInt64 due to rounding by the kernel.
	// TODO: k8s or cadvisor should export this https://github.com/google/cadvisor/blob/2b6fbacac7598e0140b5bc8428e3bdd7d86cf5b9/metrics/prometheus.go#L1969-L1971
	const maxMemorySize = uint64(1 << 62)

	return v > maxMemorySize
}

// https://github.com/kubernetes/kubernetes/blob/b47f8263e18c7b13dba33fba23187e5e0477cdbd/pkg/kubelet/stats/helper.go#L68-L71
func getAvailableBytes(memory *cg1.MemoryStat, workingSetBytes uint64) uint64 {
	// memory limit - working set bytes
	if !isMemoryUnlimited(memory.Usage.Limit) {
		return memory.Usage.Limit - workingSetBytes
	}
	return 0
}

func getAvailableBytesV2(memory *cg2.MemoryStat, workingSetBytes uint64) uint64 {
	// memory limit (memory.max) for cgroupv2 - working set bytes
	if !isMemoryUnlimited(memory.UsageLimit) {
		return memory.UsageLimit - workingSetBytes
	}
	return 0
}

func (c *criService) cpuContainerStats(ID string, isSandbox bool, stats interface{}, timestamp time.Time) (*runtime.CpuUsage, error) {
	switch metrics := stats.(type) {
	case *cg1.Metrics:
		metrics.GetCPU().GetUsage()
		if metrics.CPU != nil && metrics.CPU.Usage != nil {
			return &runtime.CpuUsage{
				Timestamp:            timestamp.UnixNano(),
				UsageCoreNanoSeconds: &runtime.UInt64Value{Value: metrics.CPU.Usage.Total},
			}, nil
		}
	case *cg2.Metrics:
		if metrics.CPU != nil {
			// convert to nano seconds
			usageCoreNanoSeconds := metrics.CPU.UsageUsec * 1000

			return &runtime.CpuUsage{
				Timestamp:            timestamp.UnixNano(),
				UsageCoreNanoSeconds: &runtime.UInt64Value{Value: usageCoreNanoSeconds},
			}, nil
		}
	default:
		return nil, fmt.Errorf("unexpected metrics type: %T from %s", metrics, reflect.TypeOf(metrics).Elem().PkgPath())
	}
	return nil, nil
}

func (c *criService) memoryContainerStats(ID string, stats interface{}, timestamp time.Time) (*runtime.MemoryUsage, error) {
	switch metrics := stats.(type) {
	case *cg1.Metrics:
		if metrics.Memory != nil && metrics.Memory.Usage != nil {
			workingSetBytes := getWorkingSet(metrics.Memory)

			return &runtime.MemoryUsage{
				Timestamp: timestamp.UnixNano(),
				WorkingSetBytes: &runtime.UInt64Value{
					Value: workingSetBytes,
				},
				AvailableBytes:  &runtime.UInt64Value{Value: getAvailableBytes(metrics.Memory, workingSetBytes)},
				UsageBytes:      &runtime.UInt64Value{Value: metrics.Memory.Usage.Usage},
				RssBytes:        &runtime.UInt64Value{Value: metrics.Memory.TotalRSS},
				PageFaults:      &runtime.UInt64Value{Value: metrics.Memory.TotalPgFault},
				MajorPageFaults: &runtime.UInt64Value{Value: metrics.Memory.TotalPgMajFault},
			}, nil
		}
	case *cg2.Metrics:
		if metrics.Memory != nil {
			workingSetBytes := getWorkingSetV2(metrics.Memory)

			return &runtime.MemoryUsage{
				Timestamp: timestamp.UnixNano(),
				WorkingSetBytes: &runtime.UInt64Value{
					Value: workingSetBytes,
				},
				AvailableBytes: &runtime.UInt64Value{Value: getAvailableBytesV2(metrics.Memory, workingSetBytes)},
				UsageBytes:     &runtime.UInt64Value{Value: metrics.Memory.Usage},
				// Use Anon memory for RSS as cAdvisor on cgroupv2
				// see https://github.com/google/cadvisor/blob/a9858972e75642c2b1914c8d5428e33e6392c08a/container/libcontainer/handler.go#L799
				RssBytes:        &runtime.UInt64Value{Value: metrics.Memory.Anon},
				PageFaults:      &runtime.UInt64Value{Value: metrics.Memory.Pgfault},
				MajorPageFaults: &runtime.UInt64Value{Value: metrics.Memory.Pgmajfault},
			}, nil
		}
	default:
		return nil, fmt.Errorf("unexpected metrics type: %T from %s", metrics, reflect.TypeOf(metrics).Elem().PkgPath())
	}
	return nil, nil
}
