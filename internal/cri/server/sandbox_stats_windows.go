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

	"github.com/Microsoft/hcsshim"
	wstats "github.com/Microsoft/hcsshim/cmd/containerd-shim-runhcs-v1/stats"
	"github.com/Microsoft/hcsshim/hcn"
	"github.com/containerd/containerd/api/services/tasks/v1"
	"github.com/containerd/containerd/api/types"
	containerstore "github.com/containerd/containerd/v2/internal/cri/store/container"
	sandboxstore "github.com/containerd/containerd/v2/internal/cri/store/sandbox"
	"github.com/containerd/containerd/v2/internal/cri/store/stats"
	ctrdutil "github.com/containerd/containerd/v2/internal/cri/util"
	"github.com/containerd/containerd/v2/pkg/protobuf"
	"github.com/containerd/errdefs"
	"github.com/containerd/log"
	"github.com/containerd/typeurl/v2"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func (c *criService) podSandboxStats(
	ctx context.Context,
	sandbox sandboxstore.Sandbox) (*runtime.PodSandboxStats, error) {
	meta := sandbox.Metadata

	if sandbox.Status.Get().State != sandboxstore.StateReady {
		return nil, fmt.Errorf("failed to get pod sandbox stats since sandbox container %q is not in ready state: %w", meta.ID, errdefs.ErrUnavailable)
	}

	timestamp := time.Now()
	podSandboxStats := &runtime.PodSandboxStats{
		Windows: &runtime.WindowsPodSandboxStats{},
		Attributes: &runtime.PodSandboxAttributes{
			Id:          meta.ID,
			Metadata:    meta.Config.GetMetadata(),
			Labels:      meta.Config.GetLabels(),
			Annotations: meta.Config.GetAnnotations(),
		},
	}

	metrics, containers, err := c.listWindowsMetricsForSandbox(ctx, sandbox)
	if err != nil {
		return nil, fmt.Errorf("failed to obtain container stats during podSandboxStats call: %w", err)
	}

	statsMap, err := convertMetricsToWindowsStats(metrics, sandbox)
	if err != nil {
		return nil, fmt.Errorf("failed to convert stats: %w", err)
	}

	podCPU, containerStats, err := c.toPodSandboxStats(sandbox, statsMap, containers, timestamp)
	if err != nil {
		return nil, fmt.Errorf("failed to convert container stats during podSandboxStats call: %w", err)
	}
	podSandboxStats.Windows.Cpu = podCPU.Cpu
	podSandboxStats.Windows.Memory = podCPU.Memory
	podSandboxStats.Windows.Containers = containerStats
	podSandboxStats.Windows.Network = windowsNetworkUsage(ctx, sandbox, timestamp)

	pidCount, err := c.getSandboxPidCount(ctx, sandbox)
	if err != nil {
		return nil, fmt.Errorf("failed to get pid count: %w", err)
	}

	podSandboxStats.Windows.Process = &runtime.WindowsProcessUsage{
		Timestamp:    timestamp.UnixNano(),
		ProcessCount: &runtime.UInt64Value{Value: pidCount},
	}

	c.saveSandBoxMetrics(podSandboxStats.Attributes.Id, podSandboxStats)

	return podSandboxStats, nil
}

func convertMetricsToWindowsStats(metrics []*types.Metric, sandbox sandboxstore.Sandbox) (map[string]*wstats.Statistics, error) {
	isHostProcess := sandbox.Config.GetWindows().GetSecurityContext().GetHostProcess()

	statsMap := make(map[string]*wstats.Statistics)
	for _, stat := range metrics {
		containerStatsData, err := typeurl.UnmarshalAny(stat.Data)
		if err != nil {
			return nil, fmt.Errorf("failed to extract metrics for container with id %s: %w", stat.ID, err)
		}

		// extract the metrics if available for this container
		// containerStatsData can be nil for pods that don't have an actual podsandbox container such as HPC
		// In the case of HostProcess sandbox container we will use the nil value for the statsmap which is used later
		// otherwise return an error since we should have gotten stats
		containerStats, ok := containerStatsData.(*wstats.Statistics)
		//nolint:staticcheck // QF1001: could apply De Morgan's law (staticcheck)
		if !ok && !(isHostProcess && sandbox.ID == stat.ID) {
			return nil, fmt.Errorf("failed to extract metrics for container with id %s: %w", stat.ID, err)
		}

		statsMap[stat.ID] = containerStats
	}
	return statsMap, nil
}

func (c *criService) toPodSandboxStats(sandbox sandboxstore.Sandbox, statsMap map[string]*wstats.Statistics, containers []containerstore.Container, timestamp time.Time) (*runtime.WindowsContainerStats, []*runtime.WindowsContainerStats, error) {
	podMetric, ok := statsMap[sandbox.ID]
	if !ok {
		return nil, nil, fmt.Errorf("failed to find container metric for pod with id %s", sandbox.ID)
	}

	ociRuntime, err := c.config.GetSandboxRuntime(sandbox.Config, sandbox.RuntimeHandler)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get runtimeHandler %q: %w", sandbox.RuntimeHandler, err)
	}
	snapshotter := c.RuntimeSnapshotter(ctrdutil.NamespacedContext(), ociRuntime)

	podRuntimeStats, err := c.convertToCRIStats(podMetric)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to covert container metrics for sandbox with id %s: %w", sandbox.ID, err)
	}

	windowsContainerStats := make([]*runtime.WindowsContainerStats, 0, len(statsMap))
	for _, cntr := range containers {
		containerMetric := statsMap[cntr.ID]

		if cntr.Status.Get().State() != runtime.ContainerState_CONTAINER_RUNNING {
			// containers that are just created, in a failed state or exited (init containers) will not have stats
			log.L.Warnf("failed to get container stats since container %q is not in running state", cntr.ID)
			continue
		}

		if containerMetric == nil {
			log.L.Warnf("no metrics found for container %q", cntr.ID)
			continue
		}

		containerStats, err := c.convertToCRIStats(containerMetric)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to convert metrics for container with id %s: %w", cntr.ID, err)
		}

		// Calculate NanoCores for container
		if containerStats.Cpu != nil && containerStats.Cpu.UsageCoreNanoSeconds != nil {
			nanoCoreUsage := getUsageNanoCores(containerStats.Cpu.UsageCoreNanoSeconds.Value, cntr.Stats, containerStats.Cpu.Timestamp)
			containerStats.Cpu.UsageNanoCores = &runtime.UInt64Value{Value: nanoCoreUsage}
		}

		// On Windows we need to add up all the podStatsData to get the Total for the Pod as there isn't something
		// like a parent cgroup that queried for all the pod podStatsData
		appendCPUPodStats(podRuntimeStats, containerStats, timestamp)
		appendMemoryPodStats(podRuntimeStats, containerStats, timestamp)

		// If snapshotstore doesn't have cached snapshot information
		// set WritableLayer usage to zero
		var usedBytes uint64
		sn, err := c.GetSnapshot(cntr.ID, snapshotter)
		if err == nil {
			usedBytes = sn.Size
		}
		containerStats.WritableLayer = &runtime.WindowsFilesystemUsage{
			Timestamp: sn.Timestamp,
			FsId: &runtime.FilesystemIdentifier{
				Mountpoint: c.imageFSPaths[snapshotter],
			},
			UsedBytes: &runtime.UInt64Value{Value: usedBytes},
		}

		containerStats.Attributes = &runtime.ContainerAttributes{
			Id:          cntr.ID,
			Metadata:    cntr.Config.GetMetadata(),
			Labels:      cntr.Config.GetLabels(),
			Annotations: cntr.Config.GetAnnotations(),
		}

		windowsContainerStats = append(windowsContainerStats, containerStats)
	}

	// Calculate NanoCores for pod after adding containers cpu including the pods cpu
	if podRuntimeStats.Cpu != nil && podRuntimeStats.Cpu.UsageCoreNanoSeconds != nil {
		nanoCoreUsage := getUsageNanoCores(podRuntimeStats.Cpu.UsageCoreNanoSeconds.Value, sandbox.Stats, podRuntimeStats.Cpu.Timestamp)
		podRuntimeStats.Cpu.UsageNanoCores = &runtime.UInt64Value{Value: nanoCoreUsage}
	}

	return podRuntimeStats, windowsContainerStats, nil
}

func appendCPUPodStats(podRuntimeStats *runtime.WindowsContainerStats, containerRunTimeStats *runtime.WindowsContainerStats, timestamp time.Time) {
	// protect against missing stats in case container hasn't started yet
	if containerRunTimeStats.Cpu == nil || containerRunTimeStats.Cpu.UsageCoreNanoSeconds == nil {
		return
	}

	// It is possible the pod sandbox might not be populated with values if it doesn't exist
	// HostProcess pods are an example where there is no actual pod sandbox running and therefor no stats
	if podRuntimeStats.Cpu == nil {
		podRuntimeStats.Cpu = &runtime.WindowsCpuUsage{
			Timestamp:            timestamp.UnixNano(),
			UsageCoreNanoSeconds: &runtime.UInt64Value{Value: 0},
		}
	}

	if podRuntimeStats.Cpu.UsageCoreNanoSeconds == nil {
		podRuntimeStats.Cpu.UsageCoreNanoSeconds = &runtime.UInt64Value{Value: 0}
	}

	podRuntimeStats.Cpu.UsageCoreNanoSeconds.Value += containerRunTimeStats.Cpu.UsageCoreNanoSeconds.Value
}

func appendMemoryPodStats(podRuntimeStats *runtime.WindowsContainerStats, containerRunTimeStats *runtime.WindowsContainerStats, timestamp time.Time) {
	// protect against missing stats in case container hasn't started yet
	if containerRunTimeStats.Memory == nil {
		return
	}

	// It is possible the pod sandbox might not be populated with values if it doesn't exist
	// HostProcess pods are an example where there is no actual pod sandbox running and therefor no stats
	if podRuntimeStats.Memory == nil {
		podRuntimeStats.Memory = &runtime.WindowsMemoryUsage{
			Timestamp:         timestamp.UnixNano(),
			WorkingSetBytes:   &runtime.UInt64Value{Value: 0},
			AvailableBytes:    &runtime.UInt64Value{Value: 0},
			PageFaults:        &runtime.UInt64Value{Value: 0},
			CommitMemoryBytes: &runtime.UInt64Value{Value: 0},
		}
	}

	if containerRunTimeStats.Memory.WorkingSetBytes != nil {
		if podRuntimeStats.Memory.WorkingSetBytes == nil {
			podRuntimeStats.Memory.WorkingSetBytes = &runtime.UInt64Value{Value: 0}
		}
		podRuntimeStats.Memory.WorkingSetBytes.Value += containerRunTimeStats.Memory.WorkingSetBytes.Value
	}

	if containerRunTimeStats.Memory.AvailableBytes != nil {
		if podRuntimeStats.Memory.AvailableBytes == nil {
			podRuntimeStats.Memory.AvailableBytes = &runtime.UInt64Value{Value: 0}
		}
		podRuntimeStats.Memory.AvailableBytes.Value += containerRunTimeStats.Memory.AvailableBytes.Value
	}

	if containerRunTimeStats.Memory.PageFaults != nil {
		if podRuntimeStats.Memory.PageFaults == nil {
			podRuntimeStats.Memory.PageFaults = &runtime.UInt64Value{Value: 0}
		}
		podRuntimeStats.Memory.PageFaults.Value += containerRunTimeStats.Memory.PageFaults.Value
	}
}

func (c *criService) listWindowsMetricsForSandbox(ctx context.Context, sandbox sandboxstore.Sandbox) ([]*types.Metric, []containerstore.Container, error) {
	req := &tasks.MetricsRequest{}
	var containers []containerstore.Container
	for _, cntr := range c.containerStore.List() {
		if cntr.SandboxID != sandbox.ID {
			continue
		}
		containers = append(containers, cntr)
		req.Filters = append(req.Filters, "id=="+cntr.ID)
	}

	//add sandbox container as well
	req.Filters = append(req.Filters, "id=="+sandbox.ID)

	resp, err := c.client.TaskService().Metrics(ctx, req)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to fetch metrics for tasks: %w", err)
	}
	return resp.Metrics, containers, nil
}

func (c *criService) convertToCRIStats(stats *wstats.Statistics) (*runtime.WindowsContainerStats, error) {
	var cs runtime.WindowsContainerStats
	// the metric should exist but stats or stats.container will be nil for HostProcess sandbox containers
	// this can also be the case when the container has not started yet
	if stats != nil && stats.Container != nil {
		wstats := stats.GetWindows()
		if wstats == nil {
			return nil, fmt.Errorf("windows stats is empty")
		}
		if wstats.Processor != nil {
			cs.Cpu = &runtime.WindowsCpuUsage{
				Timestamp:            (protobuf.FromTimestamp(wstats.Timestamp)).UnixNano(),
				UsageCoreNanoSeconds: &runtime.UInt64Value{Value: wstats.Processor.TotalRuntimeNS},
			}
		}

		if wstats.Memory != nil {
			cs.Memory = &runtime.WindowsMemoryUsage{
				Timestamp: (protobuf.FromTimestamp(wstats.Timestamp)).UnixNano(),
				WorkingSetBytes: &runtime.UInt64Value{
					Value: wstats.Memory.MemoryUsagePrivateWorkingSetBytes,
				},
				CommitMemoryBytes: &runtime.UInt64Value{
					Value: wstats.Memory.MemoryUsageCommitBytes,
				},
			}
		}

	}
	return &cs, nil
}

func getUsageNanoCores(usageCoreNanoSeconds uint64, oldStats *stats.ContainerStats, newtimestamp int64) uint64 {
	if oldStats == nil {
		return 0
	}

	nanoSeconds := newtimestamp - oldStats.Timestamp.UnixNano()

	// zero or negative interval
	if nanoSeconds <= 0 {
		return 0
	}

	return uint64(float64(usageCoreNanoSeconds-oldStats.UsageCoreNanoSeconds) /
		float64(nanoSeconds) * float64(time.Second/time.Nanosecond))
}

func windowsNetworkUsage(ctx context.Context, sandbox sandboxstore.Sandbox, timestamp time.Time) *runtime.WindowsNetworkUsage {
	eps, err := hcn.GetNamespaceEndpointIds(sandbox.NetNSPath)
	if err != nil {
		log.G(ctx).WithField("podsandboxid", sandbox.ID).WithError(err).Errorf("unable to retrieve windows endpoint metrics for netNsPath: %v", sandbox.NetNSPath)
		return nil
	}
	networkUsage := &runtime.WindowsNetworkUsage{
		Timestamp: timestamp.UnixNano(),
	}
	for _, ep := range eps {
		endpointStats, err := hcsshim.GetHNSEndpointStats(ep)
		if err != nil {
			log.G(ctx).WithError(err).Errorf("unable to gather stats for endpoint: %s", ep)
			continue
		}
		rtStats := runtime.WindowsNetworkInterfaceUsage{
			Name:             endpointStats.EndpointID,
			RxBytes:          &runtime.UInt64Value{Value: endpointStats.BytesReceived},
			RxPacketsDropped: &runtime.UInt64Value{Value: endpointStats.DroppedPacketsIncoming},
			TxBytes:          &runtime.UInt64Value{Value: endpointStats.BytesSent},
			TxPacketsDropped: &runtime.UInt64Value{Value: endpointStats.DroppedPacketsOutgoing},
		}
		networkUsage.Interfaces = append(networkUsage.Interfaces, &rtStats)

		// if the default interface isn't set add it.
		// We don't have a way to determine the default interface in windows
		if networkUsage.DefaultInterface == nil {
			networkUsage.DefaultInterface = &rtStats
		}
	}

	return networkUsage
}

func (c *criService) saveSandBoxMetrics(sandboxID string, sandboxStats *runtime.PodSandboxStats) error {
	// we may not have stats since container hasn't started yet so skip saving to cache
	if sandboxStats == nil || sandboxStats.Windows == nil || sandboxStats.Windows.Cpu == nil ||
		sandboxStats.Windows.Cpu.UsageCoreNanoSeconds == nil {
		return nil
	}

	newStats := &stats.ContainerStats{
		UsageCoreNanoSeconds: sandboxStats.Windows.Cpu.UsageCoreNanoSeconds.Value,
		Timestamp:            time.Unix(0, sandboxStats.Windows.Cpu.Timestamp),
	}
	err := c.sandboxStore.UpdateContainerStats(sandboxID, newStats)
	if err != nil {
		return err
	}

	// We queried the stats when getting sandbox stats.  We need to save the query to cache
	for _, cntr := range sandboxStats.Windows.Containers {
		// we may not have stats since container hasn't started yet so skip saving to cache
		if cntr == nil || cntr.Cpu == nil || cntr.Cpu.UsageCoreNanoSeconds == nil {
			return nil
		}

		newStats := &stats.ContainerStats{
			UsageCoreNanoSeconds: cntr.Cpu.UsageCoreNanoSeconds.Value,
			Timestamp:            time.Unix(0, cntr.Cpu.Timestamp),
		}
		err = c.containerStore.UpdateContainerStats(cntr.Attributes.Id, newStats)
		if err != nil {
			return err
		}
	}

	return nil
}

func (c *criService) getSandboxPidCount(ctx context.Context, sandbox sandboxstore.Sandbox) (uint64, error) {
	var pidCount uint64

	// get process count inside PodSandbox for Windows
	task, err := sandbox.Container.Task(ctx, nil)
	if err != nil {
		if errdefs.IsNotFound(err) {
			return 0, nil
		}
		return 0, err
	}
	processes, err := task.Pids(ctx)
	if err != nil {
		if errdefs.IsNotFound(err) {
			return 0, nil
		}
		return 0, err
	}
	pidCount += uint64(len(processes))

	for _, cntr := range c.containerStore.List() {
		if cntr.SandboxID != sandbox.ID {
			continue
		}

		state := cntr.Status.Get().State()
		if state != runtime.ContainerState_CONTAINER_RUNNING {
			continue
		}

		task, err := cntr.Container.Task(ctx, nil)
		if err != nil {
			return 0, err
		}

		processes, err := task.Pids(ctx)
		if err != nil {
			if errdefs.IsNotFound(err) {
				continue
			}
			return 0, err
		}
		pidCount += uint64(len(processes))

	}

	return pidCount, nil
}
