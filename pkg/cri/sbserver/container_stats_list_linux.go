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
	"fmt"
	"time"

	"github.com/containerd/containerd/api/types"
	v1 "github.com/containerd/containerd/metrics/types/v1"
	v2 "github.com/containerd/containerd/metrics/types/v2"
	"github.com/containerd/containerd/protobuf"
	"github.com/containerd/typeurl/v2"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	containerstore "github.com/containerd/containerd/pkg/cri/store/container"
	"github.com/containerd/containerd/pkg/cri/store/stats"
)

func (c *criService) containerMetrics(
	meta containerstore.Metadata,
	stats *types.Metric,
) (*runtime.ContainerStats, error) {
	var cs runtime.ContainerStats
	var usedBytes, inodesUsed uint64
	sn, err := c.snapshotStore.Get(meta.ID)
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

		cpuStats, err := c.cpuContainerStats(meta.ID, false /* isSandbox */, s, protobuf.FromTimestamp(stats.Timestamp))
		if err != nil {
			return nil, fmt.Errorf("failed to obtain cpu stats: %w", err)
		}
		cs.Cpu = cpuStats

		memoryStats, err := c.memoryContainerStats(meta.ID, s, protobuf.FromTimestamp(stats.Timestamp))
		if err != nil {
			return nil, fmt.Errorf("failed to obtain memory stats: %w", err)
		}
		cs.Memory = memoryStats
	}

	return &cs, nil
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

// getWorkingSet calculates workingset memory from cgroup memory stats.
// The caller should make sure memory is not nil.
// workingset = usage - total_inactive_file
func getWorkingSet(memory *v1.MemoryStat) uint64 {
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
func getWorkingSetV2(memory *v2.MemoryStat) uint64 {
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
func getAvailableBytes(memory *v1.MemoryStat, workingSetBytes uint64) uint64 {
	// memory limit - working set bytes
	if !isMemoryUnlimited(memory.Usage.Limit) {
		return memory.Usage.Limit - workingSetBytes
	}
	return 0
}

func getAvailableBytesV2(memory *v2.MemoryStat, workingSetBytes uint64) uint64 {
	// memory limit (memory.max) for cgroupv2 - working set bytes
	if !isMemoryUnlimited(memory.UsageLimit) {
		return memory.UsageLimit - workingSetBytes
	}
	return 0
}

func (c *criService) cpuContainerStats(ID string, isSandbox bool, stats interface{}, timestamp time.Time) (*runtime.CpuUsage, error) {
	switch metrics := stats.(type) {
	case *v1.Metrics:
		if metrics.CPU != nil && metrics.CPU.Usage != nil {

			usageNanoCores, err := c.getUsageNanoCores(ID, isSandbox, metrics.CPU.Usage.Total, timestamp)
			if err != nil {
				return nil, fmt.Errorf("failed to get usage nano cores, containerID: %s: %w", ID, err)
			}

			return &runtime.CpuUsage{
				Timestamp:            timestamp.UnixNano(),
				UsageCoreNanoSeconds: &runtime.UInt64Value{Value: metrics.CPU.Usage.Total},
				UsageNanoCores:       &runtime.UInt64Value{Value: usageNanoCores},
			}, nil
		}
	case *v2.Metrics:
		if metrics.CPU != nil {
			// convert to nano seconds
			usageCoreNanoSeconds := metrics.CPU.UsageUsec * 1000

			usageNanoCores, err := c.getUsageNanoCores(ID, isSandbox, usageCoreNanoSeconds, timestamp)
			if err != nil {
				return nil, fmt.Errorf("failed to get usage nano cores, containerID: %s: %w", ID, err)
			}

			return &runtime.CpuUsage{
				Timestamp:            timestamp.UnixNano(),
				UsageCoreNanoSeconds: &runtime.UInt64Value{Value: usageCoreNanoSeconds},
				UsageNanoCores:       &runtime.UInt64Value{Value: usageNanoCores},
			}, nil
		}
	default:
		return nil, fmt.Errorf("unexpected metrics type: %v", metrics)
	}
	return nil, nil
}

func (c *criService) memoryContainerStats(ID string, stats interface{}, timestamp time.Time) (*runtime.MemoryUsage, error) {
	switch metrics := stats.(type) {
	case *v1.Metrics:
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
	case *v2.Metrics:
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
		return nil, fmt.Errorf("unexpected metrics type: %v", metrics)
	}
	return nil, nil
}
