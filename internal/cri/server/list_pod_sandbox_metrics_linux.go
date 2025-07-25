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
	"reflect"
	"sync"
	"time"

	cg1 "github.com/containerd/cgroups/v3/cgroup1/stats"
	cg2 "github.com/containerd/cgroups/v3/cgroup2/stats"
	"github.com/containerd/containerd/api/services/tasks/v1"
	sandboxstore "github.com/containerd/containerd/v2/internal/cri/store/sandbox"
	"github.com/containerd/containerd/v2/internal/cri/util"
	"github.com/containerd/errdefs"
	"github.com/containerd/log"
	"golang.org/x/time/rate"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// this is part of the other go routine that updates the map
// someone should also take care of removing deleted containers and sandboxes from the map
func (c *criService) updatePodSandboxMetrics(ctx context.Context, sandboxID string) *SandboxMetrics {
	// Always create fresh metrics instead of returning cached ones
	sm := &SandboxMetrics{
		metric: &runtime.PodSandboxMetrics{
			PodSandboxId:     sandboxID,
			Metrics:          []*runtime.Metric{},
			ContainerMetrics: []*runtime.ContainerMetrics{},
		},
	}

	// generate sandbox metrics
	request := &tasks.MetricsRequest{Filters: []string{"id==" + sandboxID}}
	resp, err := c.client.TaskService().Metrics(ctx, request)
	if err != nil {
		log.G(ctx).WithError(err).Error("failed to fetch metrics for task")
	}
	if len(resp.Metrics) != 1 {
		log.G(ctx).Errorf("unexpected metrics response: %+v", resp.Metrics)
	}
	cpu, err := c.cpuMetrics(ctx, resp.Metrics[0])
	if err != nil {
		log.G(ctx).WithError(err)
	}
	sm.metric.Metrics = append(sm.metric.Metrics, generateContainerCPUMetrics(cpu)...)

	memory, err := c.memoryMetrics(ctx, resp.Metrics[0])
	if err != nil {
		log.G(ctx).WithError(err)
	}
	sm.metric.Metrics = append(sm.metric.Metrics, generateContainerMemoryMetrics(memory)...)

	network, err := c.networkMetrics(ctx, resp.Metrics[0])
	if err != nil {
		log.G(ctx).WithError(err)
	}
	sm.metric.Metrics = append(sm.metric.Metrics, generateSandboxNetworkMetrics(network)...)

	// get metrics for each container in the sandbox
	containers := c.containerStore.List()
	for _, container := range containers {
		if container.SandboxID == sandboxID {
			metrics, err := c.listContainerMetrics(ctx, container.ID)
			if err != nil {
				log.G(ctx).WithError(err).Errorf("failed to list metrics for container %s", container.ID)
			}
			sm.metric.ContainerMetrics = append(sm.metric.ContainerMetrics, metrics)
		}
	}
	// Safely update the metrics cache
	if c.metricsServer != nil {
		c.metricsServer.mu.Lock()
		if c.metricsServer.sandboxMetrics == nil {
			c.metricsServer.sandboxMetrics = make(map[string]*SandboxMetrics)
		}
		c.metricsServer.sandboxMetrics[sandboxID] = sm
		c.metricsServer.mu.Unlock()
	}
	return sm
}

// collectPodSandboxMetrics collects metrics for a specific pod sandbox and its containers
func (c *criService) collectPodSandboxMetrics(ctx context.Context, sandbox sandboxstore.Sandbox) (*runtime.PodSandboxMetrics, error) {
	meta := sandbox.Metadata
	config := sandbox.Config

	cstatus, err := c.sandboxService.SandboxStatus(ctx, sandbox.Sandboxer, sandbox.ID, true)
	if err != nil {
		return nil, fmt.Errorf("failed getting status for sandbox %s: %w", sandbox.ID, err)
	}

	// Get sandbox stats
	stats, err := metricsForSandbox(sandbox, cstatus.Info)
	if err != nil {
		return nil, fmt.Errorf("failed getting metrics for sandbox %s: %w", sandbox.ID, err)
	}

	podMetrics := &runtime.PodSandboxMetrics{
		PodSandboxId: meta.ID,
		Metrics:      []*runtime.Metric{},
	}

	timestamp := time.Now().UnixNano()

	// Extract pod-level labels
	podName := config.GetMetadata().GetName()
	namespace := config.GetMetadata().GetNamespace()
	podLabels := []string{podName, namespace, meta.ID}

	if stats != nil {
		// Collect pod-level network metrics
		if sandbox.NetNSPath != "" {
			rxBytes, rxErrors, txBytes, txErrors, rxPackets, rxDropped, txPackets, txDropped := getContainerNetIO(ctx, sandbox.NetNSPath)

			podMetrics.Metrics = append(podMetrics.Metrics, []*runtime.Metric{
				{
					Name:        "container_network_receive_bytes_total",
					Timestamp:   timestamp,
					MetricType:  runtime.MetricType_COUNTER,
					LabelValues: append(podLabels, "eth0"),
					Value:       &runtime.UInt64Value{Value: rxBytes},
				},
				{
					Name:        "container_network_receive_packets_total",
					Timestamp:   timestamp,
					MetricType:  runtime.MetricType_COUNTER,
					LabelValues: append(podLabels, "eth0"),
					Value:       &runtime.UInt64Value{Value: rxPackets},
				},
				{
					Name:        "container_network_receive_packets_dropped_total",
					Timestamp:   timestamp,
					MetricType:  runtime.MetricType_COUNTER,
					LabelValues: append(podLabels, "eth0"),
					Value:       &runtime.UInt64Value{Value: rxDropped},
				},
				{
					Name:        "container_network_receive_errors_total",
					Timestamp:   timestamp,
					MetricType:  runtime.MetricType_COUNTER,
					LabelValues: append(podLabels, "eth0"),
					Value:       &runtime.UInt64Value{Value: rxErrors},
				},
				{
					Name:        "container_network_transmit_bytes_total",
					Timestamp:   timestamp,
					MetricType:  runtime.MetricType_COUNTER,
					LabelValues: append(podLabels, "eth0"),
					Value:       &runtime.UInt64Value{Value: txBytes},
				},
				{
					Name:        "container_network_transmit_packets_total",
					Timestamp:   timestamp,
					MetricType:  runtime.MetricType_COUNTER,
					LabelValues: append(podLabels, "eth0"),
					Value:       &runtime.UInt64Value{Value: txPackets},
				},
				{
					Name:        "container_network_transmit_packets_dropped_total",
					Timestamp:   timestamp,
					MetricType:  runtime.MetricType_COUNTER,
					LabelValues: append(podLabels, "eth0"),
					Value:       &runtime.UInt64Value{Value: txDropped},
				},
				{
					Name:        "container_network_transmit_errors_total",
					Timestamp:   timestamp,
					MetricType:  runtime.MetricType_COUNTER,
					LabelValues: append(podLabels, "eth0"),
					Value:       &runtime.UInt64Value{Value: txErrors},
				},
			}...)
		}
	}

	// Collect container metrics
	containers := c.containerStore.List()
	for _, container := range containers {
		if container.SandboxID != sandbox.ID {
			continue
		}

		containerMetrics, err := c.collectContainerMetrics(ctx, container, podName, namespace)
		if err != nil {
			log.G(ctx).WithField("containerid", container.ID).WithError(err).Debug("failed to collect container metrics")
			continue
		}

		podMetrics.ContainerMetrics = append(podMetrics.ContainerMetrics, containerMetrics)
	}

	return podMetrics, nil
}

func (c *criService) ListPodSandboxMetrics(ctx context.Context, r *runtime.ListPodSandboxMetricsRequest) (*runtime.ListPodSandboxMetricsResponse, error) {
	ctx = util.WithNamespace(ctx)
	sandboxList := c.sandboxStore.List()
	podMetrics := make([]*runtime.PodSandboxMetrics, 0)

	var mu sync.Mutex
	var wg sync.WaitGroup

	// Rate limiter to prevent overwhelming the system with concurrent requests
	limiter := rate.NewLimiter(rate.Limit(10), 10) // Allow 10 concurrent requests with burst of 10
	semaphore := make(chan struct{}, 10)           // Limit to 10 concurrent goroutines

	activeSandboxIDs := make(map[string]bool)

	for _, sandbox := range sandboxList {
		// Only collect metrics for ready sandboxes
		if sandbox.Status.Get().State != sandboxstore.StateReady {
			continue
		}

		activeSandboxIDs[sandbox.ID] = true

		// Wait for rate limiter permission
		if err := limiter.Wait(ctx); err != nil {
			log.G(ctx).WithError(err).Debug("rate limiter context cancelled")
			break
		}

		semaphore <- struct{}{} // Acquire semaphore
		wg.Add(1)
		go func(sb sandboxstore.Sandbox) {
			defer wg.Done()
			defer func() { <-semaphore }() // Release semaphore

			metrics, err := c.collectPodSandboxMetrics(ctx, sb)
			if err != nil {
				switch {
				case errdefs.IsUnavailable(err), errdefs.IsNotFound(err):
					log.G(ctx).WithField("podsandboxid", sb.ID).WithError(err).Debug("failed to get pod sandbox metrics, this is likely a transient error")
				case errdefs.IsCanceled(err):
					log.G(ctx).WithField("podsandboxid", sb.ID).WithError(err).Debug("metrics collection cancelled")
				default:
					log.G(ctx).WithField("podsandboxid", sb.ID).WithError(err).Error("failed to collect pod sandbox metrics")
				}
				return
			}

			mu.Lock()
			podMetrics = append(podMetrics, metrics)
			mu.Unlock()
		}(sandbox)
	}

	wg.Wait()

	// Clean up metrics for stopped sandboxes
	if c.metricsServer != nil {
		c.metricsServer.cleanupStoppedSandboxMetrics(activeSandboxIDs)
	}

	return &runtime.ListPodSandboxMetricsResponse{
		PodMetrics: podMetrics,
	}, nil
}

// gives the metrics for a given container in a sandbox or a given sandbox
func (c *criService) listContainerMetrics(ctx context.Context, containerID string) (*runtime.ContainerMetrics, error) {
	request := &tasks.MetricsRequest{Filters: []string{"id==" + containerID}}
	resp, err := c.client.TaskService().Metrics(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch metrics for task: %w", err)
	}
	if len(resp.Metrics) != 1 {
		return nil, fmt.Errorf("unexpected metrics response: %+v", resp.Metrics)
	}

	cm := &runtime.ContainerMetrics{
		ContainerId: containerID,
		Metrics:     make([]*runtime.Metric, 0),
	}

	cpu, err := c.cpuMetrics(ctx, resp.Metrics[0])
	if err != nil {
		log.G(ctx).WithError(err).Error("failed to get CPU metrics")
	} else {
		cm.Metrics = append(cm.Metrics, generateContainerCPUMetrics(cpu)...)
	}

	memory, err := c.memoryMetrics(ctx, resp.Metrics[0])
	if err != nil {
		log.G(ctx).WithError(err).Error("failed to get memory metrics")
	} else {
		cm.Metrics = append(cm.Metrics, generateContainerMemoryMetrics(memory)...)
	}

	diskio, err := c.diskIOMetrics(ctx, resp.Metrics[0])
	if err != nil {
		log.G(ctx).WithError(err).Error("failed to get disk I/O metrics")
	} else {
		cm.Metrics = append(cm.Metrics, generateDiskIOMetrics(diskio)...)
	}

	// network metrics are captured only at sandbox level

	// Collect filesystem metrics
	filesystem, err := c.collectFilesystemMetrics(ctx, containerID)
	if err != nil {
		log.G(ctx).WithError(err).Debug("failed to get filesystem metrics")
	} else {
		cm.Metrics = append(cm.Metrics, generateFilesystemMetrics(filesystem)...)
	}

	return cm, nil
}

func (c *criService) cpuMetrics(ctx context.Context, stats interface{}) (*containerCPUMetrics, error) {
	cm := &containerCPUMetrics{}
	switch metrics := stats.(type) {
	case *cg1.Metrics:
		if metrics.GetCPU() != nil {
			if usage := metrics.GetCPU().GetUsage(); usage != nil {
				cm.UserUsec = usage.GetUser()
				cm.SystemUsec = usage.GetKernel()
				cm.UsageUsec = usage.GetTotal()
			}
			if throttling := metrics.GetCPU().GetThrottling(); throttling != nil {
				cm.NRPeriods = throttling.GetPeriods()
				cm.NRThrottledPeriods = throttling.GetThrottledPeriods()
				cm.ThrottledUsec = throttling.GetThrottledTime()
			}
		}
		return cm, nil
	case *cg2.Metrics:
		if metrics.GetCPU() != nil {
			cm.UserUsec = metrics.CPU.GetUserUsec() * 1000
			cm.SystemUsec = metrics.CPU.GetSystemUsec() * 1000
			cm.UsageUsec = metrics.CPU.GetUsageUsec() * 1000
			cm.NRPeriods = metrics.CPU.GetNrPeriods() * 1000
			cm.NRThrottledPeriods = metrics.CPU.GetNrThrottled() * 1000
			cm.ThrottledUsec = metrics.CPU.GetThrottledUsec() * 1000
		}
		return cm, nil
	default:
		return nil, fmt.Errorf("unexpected metrics type: %T from %s", metrics, reflect.TypeOf(metrics).Elem().PkgPath())
	}
}

func (c *criService) memoryMetrics(ctx context.Context, stats interface{}) (*containerMemoryMetrics, error) {
	cm := &containerMemoryMetrics{}
	switch metrics := stats.(type) {
	case *cg1.Metrics:
		if metrics.GetMemory() != nil {
			cm.Cache = metrics.Memory.GetTotalCache()
			cm.RSS = metrics.Memory.GetTotalRSS()
			cm.FileMapped = metrics.Memory.GetMappedFile()
			cm.ActiveFile = metrics.Memory.GetTotalActiveFile()
			cm.InactiveFile = metrics.Memory.GetTotalInactiveFile()
			cm.PgFault = metrics.Memory.GetPgFault()
			cm.PgMajFault = metrics.Memory.GetPgMajFault()
			cm.WorkingSet = getWorkingSet(metrics.GetMemory())
			if usage := metrics.GetMemory().GetUsage(); usage != nil {
				cm.FailCount = usage.GetFailcnt()
				cm.MemoryUsage = usage.GetUsage()
				cm.MaxUsage = usage.GetMax()
			}
			if metrics.GetMemory().GetKernel() != nil {
				cm.KernelUsage = metrics.GetMemory().GetKernel().GetUsage()
			}
			if metrics.GetMemory().GetSwap() != nil {
				cm.Swap = metrics.GetMemory().GetSwap().GetUsage()
			}
		}
		return cm, nil
	case *cg2.Metrics:
		if metrics.GetMemory() != nil {
			cm.Cache = metrics.GetMemory().GetFile()
			cm.RSS = metrics.GetMemory().GetAnon()
			cm.KernelUsage = metrics.GetMemory().GetKernelStack()
			cm.FileMapped = metrics.GetMemory().GetFileMapped()
			cm.Swap = metrics.GetMemory().GetSwapUsage() - metrics.GetMemory().GetUsage()
			cm.MemoryUsage = metrics.GetMemory().GetUsage()
			cm.MaxUsage = metrics.GetMemory().GetMaxUsage()
			cm.ActiveFile = metrics.GetMemory().GetActiveFile()
			cm.PgFault = metrics.GetMemory().GetPgfault()
			cm.PgMajFault = metrics.GetMemory().GetPgmajfault()
			cm.WorkingSet = getWorkingSetV2(metrics.Memory)
		}
		if metrics.GetMemoryEvents() != nil {
			cm.FailCount = metrics.GetMemoryEvents().GetMax()
		}
		return cm, nil
	default:
		return nil, fmt.Errorf("unexpected metrics type: %T from %s", metrics, reflect.TypeOf(metrics).Elem().PkgPath())
	}
}

func (c *criService) networkMetrics(ctx context.Context, stats interface{}) ([]containerNetworkMetrics, error) {
	cm := make([]containerNetworkMetrics, 0)
	switch metrics := stats.(type) {
	case *cg1.Metrics:
		for _, m := range metrics.GetNetwork() {
			cm = append(cm, containerNetworkMetrics{
				Name:      m.GetName(),
				RxBytes:   m.GetRxBytes(),
				TxBytes:   m.GetTxBytes(),
				RxErrors:  m.GetRxErrors(),
				TxErrors:  m.GetTxErrors(),
				RxDropped: m.GetRxDropped(),
				TxDropped: m.GetTxDropped(),
				RxPackets: m.GetRxPackets(),
				TxPackets: m.GetTxPackets(),
			})
		}
		return cm, nil
	case *cg2.Metrics:
		for _, m := range metrics.GetNetwork() {
			cm = append(cm, containerNetworkMetrics{
				Name:      m.GetName(),
				RxBytes:   m.GetRxBytes(),
				TxBytes:   m.GetTxBytes(),
				RxErrors:  m.GetRxErrors(),
				TxErrors:  m.GetTxErrors(),
				RxDropped: m.GetRxDropped(),
				TxDropped: m.GetTxDropped(),
				RxPackets: m.GetRxPackets(),
				TxPackets: m.GetTxPackets(),
			})
		}
		return cm, nil
	default:
		return nil, fmt.Errorf("unexpected metrics type: %T from %s", metrics, reflect.TypeOf(metrics).Elem().PkgPath())
	}
}

// ioValues is a helper method for assembling per-disk and per-filesystem stats.
func ioValues(ioStats []containerPerDiskStats, ioType string) metricValues {

	values := make(metricValues, 0, len(ioStats))
	for _, stat := range ioStats {
		values = append(values, metricValue{
			value:  stat.Stats[ioType],
			labels: []string{stat.Device},
		})
	}
	return values
}

// Referred from https://github.com/google/cadvisor/blob/master/container/libcontainer/helpers.go#L123C1-L131C2

type diskKey struct {
	Major uint64
	Minor uint64
}

func diskStatsCopy0(major, minor uint64) *containerPerDiskStats {
	disk := containerPerDiskStats{
		Major: major,
		Minor: minor,
	}
	disk.Stats = make(map[string]uint64)
	return &disk
}

func diskStatsCopy1(diskStat map[diskKey]*containerPerDiskStats) []containerPerDiskStats {
	i := 0
	stat := make([]containerPerDiskStats, len(diskStat))
	for _, disk := range diskStat {
		stat[i] = *disk
		i++
	}
	return stat
}

func diskStatsCopyCG1(blkioStats []cg1.BlkIOEntry) (stat []containerPerDiskStats) {
	if len(blkioStats) == 0 {
		return
	}
	diskStat := make(map[diskKey]*containerPerDiskStats)
	for i := range blkioStats {
		major := blkioStats[i].Major
		minor := blkioStats[i].Minor
		key := diskKey{
			Major: major,
			Minor: minor,
		}
		diskp, ok := diskStat[key]
		if !ok {
			diskp = diskStatsCopy0(major, minor)
			diskStat[key] = diskp
		}
		op := blkioStats[i].Op
		if op == "" {
			op = "Count"
		}
		diskp.Stats[op] = blkioStats[i].Value
	}
	return diskStatsCopy1(diskStat)
}

func (c *criService) diskIOMetrics(ctx context.Context, stats interface{}) (*containerDiskIoMetrics, error) {
	cm := &containerDiskIoMetrics{}
	switch metrics := stats.(type) {
	case *cg1.Metrics:
		cm.IoQueued = diskStatsCopyCG1(metrics.Blkio.GetIoQueuedRecursive())
		cm.IoMerged = diskStatsCopyCG1(metrics.Blkio.GetIoMergedRecursive())
		cm.IoServiceBytes = diskStatsCopyCG1(metrics.Blkio.GetIoServiceBytesRecursive())
		cm.IoServiced = diskStatsCopyCG1(metrics.Blkio.GetIoServicedRecursive())
		cm.IoTime = diskStatsCopyCG1(metrics.Blkio.GetIoTimeRecursive())
	case *cg2.Metrics:
		// TODO
	default:
		return nil, fmt.Errorf("unexpected metrics type: %T from %s", metrics, reflect.TypeOf(metrics).Elem().PkgPath())
	}

}

func generateSandboxNetworkMetrics(metrics []containerNetworkMetrics) []*runtime.Metric {
	nm := containerNetworkMetrics{}
	// TODO? should we have separate metrics per interface with different labels or add them together
	//  and expose it
	for _, m := range metrics {
		nm.RxBytes += m.RxBytes
		nm.RxPackets += m.RxPackets
		nm.RxErrors += m.RxErrors
		nm.RxDropped += m.RxDropped
		nm.TxBytes += m.TxBytes
		nm.TxPackets += m.TxPackets
		nm.TxErrors += m.TxErrors
		nm.TxDropped += m.TxDropped
	}
	networkMetrics := []*containerMetric{
		{
			desc: &runtime.MetricDescriptor{
				Name: "container_network_receive_bytes_total",
				Help: "Cumulative count of bytes received",
				//LabelKeys: append(baseLabelKeys, "interface"),
			},
			valueFunc: func() metricValues {
				return metricValues{{
					value:      nm.RxBytes,
					metricType: runtime.MetricType_COUNTER,
				}}
			},
		},
		{
			desc: &runtime.MetricDescriptor{
				Name: "container_network_receive_packets_total",
				Help: "Cumulative count of packets received",
			},
			valueFunc: func() metricValues {
				return metricValues{{
					value:      nm.RxPackets,
					metricType: runtime.MetricType_COUNTER,
				}}
			},
		},
		{
			desc: &runtime.MetricDescriptor{
				Name: "container_network_receive_packets_dropped_total",
				Help: "Cumulative count of packets dropped while receiving packets",
			},
			valueFunc: func() metricValues {
				return metricValues{{
					value:      nm.RxDropped,
					metricType: runtime.MetricType_COUNTER,
				}}
			},
		},
		{
			desc: &runtime.MetricDescriptor{
				Name: "container_network_receive_errors_total",
				Help: "Cumulative count of errors encountered while receiving",
			},
			valueFunc: func() metricValues {
				return metricValues{{
					value:      nm.RxErrors,
					metricType: runtime.MetricType_COUNTER,
				}}
			},
		},
		{
			desc: &runtime.MetricDescriptor{
				Name: "container_network_transmit_bytes_total",
				Help: "Cumulative count of bytes transmitted",
			},
			valueFunc: func() metricValues {
				return metricValues{{
					value:      nm.TxBytes,
					metricType: runtime.MetricType_COUNTER,
				}}
			},
		},
		{
			desc: &runtime.MetricDescriptor{
				Name: "container_network_transmit_packets_total",
				Help: "Cumulative count of packets transmitted",
			},
			valueFunc: func() metricValues {
				return metricValues{{
					value:      nm.TxPackets,
					metricType: runtime.MetricType_COUNTER,
				}}
			},
		},
		{
			desc: &runtime.MetricDescriptor{
				Name: "container_network_transmit_packets_dropped_total",
				Help: "Cumulative count of packets dropped while transmitting",
			},
			valueFunc: func() metricValues {
				return metricValues{{
					value:      nm.TxDropped,
					metricType: runtime.MetricType_COUNTER,
				}}
			},
		},
		{
			desc: &runtime.MetricDescriptor{
				Name: "container_network_transmit_errors_total",
				Help: "Cumulative count of errors encountered while transmitting",
			},
			valueFunc: func() metricValues {
				return metricValues{{
					value:      nm.TxErrors,
					metricType: runtime.MetricType_COUNTER,
				}}
			},
		},
	}
	return computeSandboxMetrics(networkMetrics, "network")
}

func generateContainerCPUMetrics(metrics *containerCPUMetrics) []*runtime.Metric {
	cpuMetrics := []*containerMetric{
		{
			desc: &runtime.MetricDescriptor{
				Name: "container_cpu_usage_seconds_total",
				Help: "Cumulative user CPU time consumed in seconds",
			},
			valueFunc: func() metricValues {
				return metricValues{{
					value:      metrics.UsageUsec,
					metricType: runtime.MetricType_COUNTER,
				}}
			},
		},
		{
			desc: &runtime.MetricDescriptor{
				Name: "container_cpu_user_seconds_total",
				Help: "Cumulative user CPU time consumed in seconds",
			},
			valueFunc: func() metricValues {
				return metricValues{{
					value:      metrics.UserUsec,
					metricType: runtime.MetricType_COUNTER,
				}}
			},
		},
		{
			desc: &runtime.MetricDescriptor{
				Name: "container_cpu_system_seconds_total",
				Help: "Cumulative system CPU time consumed in seconds",
			},
			valueFunc: func() metricValues {
				return metricValues{{
					value:      metrics.SystemUsec,
					metricType: runtime.MetricType_COUNTER,
				}}
			},
		},
		{
			desc: &runtime.MetricDescriptor{
				Name: "container_cpu_cfs_periods_total",
				Help: "Number of elapsed enforcement period intervals.",
			},
			valueFunc: func() metricValues {
				return metricValues{{
					value:      metrics.NRPeriods,
					metricType: runtime.MetricType_COUNTER,
				}}
			},
		},
		{
			desc: &runtime.MetricDescriptor{
				Name: "container_cpu_cfs_throttled_periods_total",
				Help: "Number of throttled period intervals.",
			},
			valueFunc: func() metricValues {
				return metricValues{{
					value:      metrics.NRThrottledPeriods,
					metricType: runtime.MetricType_COUNTER,
				}}
			},
		},
		{
			desc: &runtime.MetricDescriptor{
				Name: "container_cpu_cfs_throttled_seconds_total",
				Help: "Total time duration the container has been throttled.",
			},
			valueFunc: func() metricValues {
				return metricValues{{
					value: metrics.ThrottledUsec,
				}}
			},
		},
	}

	return computeSandboxMetrics(cpuMetrics, "cpu")

}

func generateContainerMemoryMetrics(metrics *containerMemoryMetrics) []*runtime.Metric {
	memoryMetrics := []*containerMetric{
		{
			desc: &runtime.MetricDescriptor{
				Name: "container_memory_cache",
				Help: "Number of bytes of page cache memory.",
			},
			valueFunc: func() metricValues {
				return metricValues{{
					value:      metrics.Cache,
					metricType: runtime.MetricType_GAUGE,
				}}
			},
		},
		{
			desc: &runtime.MetricDescriptor{
				Name: "container_memory_rss",
				Help: "Size of RSS in bytes.",
			},
			valueFunc: func() metricValues {
				return metricValues{{
					value:      metrics.RSS,
					metricType: runtime.MetricType_GAUGE,
				}}
			},
		},
		{
			desc: &runtime.MetricDescriptor{
				Name: "container_memory_swap",
				Help: "Container swap usage in bytes.",
			},
			valueFunc: func() metricValues {
				return metricValues{{
					value:      metrics.Swap,
					metricType: runtime.MetricType_GAUGE,
				}}
			},
		},
		{
			desc: &runtime.MetricDescriptor{
				Name: "container_memory_kernel_usage",
				Help: "Size of kernel memory allocated in bytes.",
			},
			valueFunc: func() metricValues {
				return metricValues{{
					value:      metrics.KernelUsage,
					metricType: runtime.MetricType_GAUGE,
				}}
			},
		},
		{
			desc: &runtime.MetricDescriptor{
				Name: "container_memory_mapped_file",
				Help: "Size of memory mapped files in bytes.",
			},
			valueFunc: func() metricValues {
				return metricValues{{
					value:      metrics.FileMapped,
					metricType: runtime.MetricType_GAUGE,
				}}
			},
		},
		{
			desc: &runtime.MetricDescriptor{
				Name: "container_memory_failcnt",
				Help: "Number of memory usage hits limits",
			},
			valueFunc: func() metricValues {
				return metricValues{{
					value:      metrics.FailCount,
					metricType: runtime.MetricType_COUNTER,
				}}
			},
		},
		{
			desc: &runtime.MetricDescriptor{
				Name: "container_memory_usage_bytes",
				Help: "Current memory usage in bytes, including all memory regardless of when it was accessed",
			},
			valueFunc: func() metricValues {
				return metricValues{{
					value:      metrics.MemoryUsage,
					metricType: runtime.MetricType_GAUGE,
				}}
			},
		},
		{
			desc: &runtime.MetricDescriptor{
				Name: "container_memory_max_usage_bytes",
				Help: "Maximum memory usage recorded in bytes",
			},
			valueFunc: func() metricValues {
				return metricValues{{
					value:      metrics.MaxUsage,
					metricType: runtime.MetricType_GAUGE,
				}}
			},
		},
		{
			desc: &runtime.MetricDescriptor{
				Name: "container_memory_working_set_bytes",
				Help: "Current working set in bytes.",
			},
			valueFunc: func() metricValues {
				return metricValues{{
					value:      metrics.WorkingSet,
					metricType: runtime.MetricType_GAUGE,
				}}
			},
		},
		{
			desc: &runtime.MetricDescriptor{
				Name: "container_memory_total_active_file_bytes",
				Help: "Current total active file in bytes.",
			},
			valueFunc: func() metricValues {
				return metricValues{{
					value:      metrics.ActiveFile,
					metricType: runtime.MetricType_GAUGE,
				}}
			},
		},
		{
			desc: &runtime.MetricDescriptor{
				Name: "container_memory_total_inactive_file_bytes",
				Help: "Current total inactive file in bytes.",
			},
			valueFunc: func() metricValues {
				return metricValues{{
					value: metrics.InactiveFile,
				}}
			},
		},
	}
	return computeSandboxMetrics(memoryMetrics, "memory")
}

func generateDiskIOMetrics(metrics *containerDiskIoMetrics) []*runtime.Metric {
	diskIOMetrics := []*containerMetric{
		{
			desc: &runtime.MetricDescriptor{
				Name: "container_fs_reads_bytes_total",
				Help: "Cumulative count of bytes read",
			},
			valueFunc: func() metricValues {
				return ioValues(metrics.IoServiceBytes, "Read")
			},
		},
		{
			desc: &runtime.MetricDescriptor{
				Name: "container_fs_writes_bytes_total",
				Help: "Cumulative count of bytes written",
			},
			valueFunc: func() metricValues {
				return ioValues(metrics.IoServiceBytes, "Write")
			},
		},
	}
	return computeSandboxMetrics(diskIOMetrics, "diskIO")
}

// computeSandboxMetrics computes the metrics for both pod and container sandbox.
func computeSandboxMetrics(metrics []*containerMetric, metricName string) []*runtime.Metric {
	values := []string{metricName}
	calculatedMetrics := make([]*runtime.Metric, 0, len(metrics))

	for _, m := range metrics {
		for _, v := range m.valueFunc() {
			newMetric := &runtime.Metric{
				Name:        m.desc.Name,
				Timestamp:   time.Now().UnixNano(),
				MetricType:  v.metricType,
				Value:       &runtime.UInt64Value{Value: v.value},
				LabelValues: append(values, v.labels...),
			}
			calculatedMetrics = append(calculatedMetrics, newMetric)
		}
	}

	return calculatedMetrics
}
