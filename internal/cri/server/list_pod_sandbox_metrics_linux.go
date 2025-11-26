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
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	cg1 "github.com/containerd/cgroups/v3/cgroup1/stats"
	cg2 "github.com/containerd/cgroups/v3/cgroup2/stats"
	"github.com/containerd/errdefs"
	"github.com/containerd/log"
	"github.com/containerd/typeurl/v2"
	"golang.org/x/sync/errgroup"
	"golang.org/x/time/rate"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/containers"
	containerstore "github.com/containerd/containerd/v2/internal/cri/store/container"
	sandboxstore "github.com/containerd/containerd/v2/internal/cri/store/sandbox"
	"github.com/containerd/containerd/v2/internal/cri/util"
	"github.com/containerd/containerd/v2/plugins"
)

// Rate limiter to prevent overwhelming the system with concurrent requests
var limiter = rate.NewLimiter(rate.Limit(10), 10) // Allow 10 concurrent requests with burst of 10

// ListPodSandboxMetrics gets pod sandbox metrics from CRI Runtime
func (c *criService) ListPodSandboxMetrics(ctx context.Context, r *runtime.ListPodSandboxMetricsRequest) (*runtime.ListPodSandboxMetricsResponse, error) {
	ctx = util.WithNamespace(ctx)

	// Create errgroup with context and limit concurrency to 10
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(10)

	// Fetch all containers before executing any metric collection
	// to correctly map them to each sandbox.
	sandboxContainerMap := make(map[string][]containerstore.Container)
	for _, container := range c.containerStore.List() {
		sandboxContainerMap[container.SandboxID] = append(sandboxContainerMap[container.SandboxID], container)
	}

	var (
		podSandboxMetrics []*runtime.PodSandboxMetrics
		metricsMu         sync.Mutex
	)
	for _, sandbox := range c.sandboxStore.List() {
		// Only collect metrics for ready sandboxes
		if sandbox.Status.Get().State != sandboxstore.StateReady {
			continue
		}

		// Wait for rate limiter permission
		if err := limiter.Wait(ctx); err != nil {
			log.G(ctx).WithError(err).Debug("rate limiter context cancelled")
			return nil, err
		}

		g.Go(func() error {
			// Extract pod-level labels
			baseLabels := []string{sandbox.Config.GetMetadata().GetName(), sandbox.Config.GetMetadata().GetNamespace()}

			sandboxMetrics, err := c.collectPodSandboxMetrics(gctx, sandbox, baseLabels)
			if err != nil {
				switch {
				case errdefs.IsUnavailable(err), errdefs.IsNotFound(err):
					log.G(gctx).WithField("podsandboxid", sandbox.ID).WithError(err).Error("failed to get pod sandbox metrics, this is likely a transient error")
					// Don't return error for transient issues, just log and continue
					return nil
				case errdefs.IsCanceled(err):
					log.G(gctx).WithField("podsandboxid", sandbox.ID).WithError(err).Debug("metrics collection cancelled")
					// Return the cancellation error to stop other goroutines
					return err
				default:
					log.G(gctx).WithField("podsandboxid", sandbox.ID).WithError(err).Error("failed to collect pod sandbox metrics")
					// Don't return error for individual failures, just log and continue
					return nil
				}
			}

			for _, container := range sandboxContainerMap[sandbox.ID] {
				containerMetrics, err := c.collectContainerMetrics(ctx, container, baseLabels)
				if err != nil {
					switch {
					case errdefs.IsUnavailable(err), errdefs.IsNotFound(err):
						log.G(gctx).WithField("podsandboxid", sandbox.ID).WithField("containerid", container.ID).WithError(err).Error("failed to get container metrics, this is likely a transient error")
						// Don't return error for transient issues, just log and continue
						return nil
					case errdefs.IsCanceled(err):
						log.G(gctx).WithField("podsandboxid", sandbox.ID).WithField("containerid", container.ID).WithError(err).Debug("metrics collection cancelled")
						// Return the cancellation error to stop other goroutines
						return err
					default:
						log.G(gctx).WithField("podsandboxid", sandbox.ID).WithField("containerid", container.ID).WithError(err).Error("failed to collect container metrics")
						// Don't return error for individual failures, just log and continue
						return nil
					}
				}

				sandboxMetrics.ContainerMetrics = append(sandboxMetrics.ContainerMetrics, containerMetrics)
			}

			// If we already exited the loop because of a canceled request
			// we don't want to write to a potentially closed channel
			if gctx.Err() != nil {
				return gctx.Err()
			}

			metricsMu.Lock()
			podSandboxMetrics = append(podSandboxMetrics, sandboxMetrics)
			metricsMu.Unlock()
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		// log the error and return the metrics that we have collected so far
		log.G(ctx).WithError(err).Error("error during metrics collection, returning partial results")
	}

	return &runtime.ListPodSandboxMetricsResponse{
		PodMetrics: podSandboxMetrics,
	}, nil
}

// collectPodSandboxMetrics collects metrics for a specific pod sandbox
func (c *criService) collectPodSandboxMetrics(ctx context.Context, sandbox sandboxstore.Sandbox, labels []string) (*runtime.PodSandboxMetrics, error) {
	podMetrics := &runtime.PodSandboxMetrics{
		PodSandboxId: sandbox.Metadata.ID,
	}

	// container and image label is set to "" as these are pod level metrics
	podLabels := append(append([]string(nil), labels...), "", sandbox.ID, "")
	timestamp := time.Now().UnixNano()

	// Collect pod-level network metrics
	if sandbox.NetNSPath != "" {
		linkStats, err := getContainerNetIO(ctx, sandbox.NetNSPath)
		if err != nil {
			return nil, err
		}

		podNetworkLabels := append(append([]string(nil), podLabels...), defaultIfName)
		podMetrics.Metrics = append(podMetrics.Metrics, []*runtime.Metric{
			{
				Name:        containerNetworkReceiveBytesTotal.Name,
				Timestamp:   timestamp,
				MetricType:  runtime.MetricType_COUNTER,
				LabelValues: podNetworkLabels,
				Value:       &runtime.UInt64Value{Value: linkStats.RxBytes},
			},
			{
				Name:        containerNetworkReceivePacketsTotal.Name,
				Timestamp:   timestamp,
				MetricType:  runtime.MetricType_COUNTER,
				LabelValues: podNetworkLabels,
				Value:       &runtime.UInt64Value{Value: linkStats.RxPackets},
			},
			{
				Name:        containerNetworkReceivePacketsDroppedTotal.Name,
				Timestamp:   timestamp,
				MetricType:  runtime.MetricType_COUNTER,
				LabelValues: podNetworkLabels,
				Value:       &runtime.UInt64Value{Value: linkStats.RxDropped},
			},
			{
				Name:        containerNetworkReceiveErrorsTotal.Name,
				Timestamp:   timestamp,
				MetricType:  runtime.MetricType_COUNTER,
				LabelValues: podNetworkLabels,
				Value:       &runtime.UInt64Value{Value: linkStats.RxErrors},
			},
			{
				Name:        containerNetworkTransmitBytesTotal.Name,
				Timestamp:   timestamp,
				MetricType:  runtime.MetricType_COUNTER,
				LabelValues: podNetworkLabels,
				Value:       &runtime.UInt64Value{Value: linkStats.TxBytes},
			},
			{
				Name:        containerNetworkTransmitPacketsTotal.Name,
				Timestamp:   timestamp,
				MetricType:  runtime.MetricType_COUNTER,
				LabelValues: podNetworkLabels,
				Value:       &runtime.UInt64Value{Value: linkStats.TxPackets},
			},
			{
				Name:        containerNetworkTransmitPacketsDroppedTotal.Name,
				Timestamp:   timestamp,
				MetricType:  runtime.MetricType_COUNTER,
				LabelValues: podNetworkLabels,
				Value:       &runtime.UInt64Value{Value: linkStats.TxDropped},
			},
			{
				Name:        containerNetworkTransmitErrorsTotal.Name,
				Timestamp:   timestamp,
				MetricType:  runtime.MetricType_COUNTER,
				LabelValues: podNetworkLabels,
				Value:       &runtime.UInt64Value{Value: linkStats.TxErrors},
			},
		}...)
	}

	return podMetrics, nil
}

func (c *criService) collectContainerMetrics(ctx context.Context, container containerstore.Container, labels []string) (*runtime.ContainerMetrics, error) {
	task, err := container.Container.Task(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get task for container %s: %w", container.ID, err)
	}

	taskMetrics, err := task.Metrics(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get metrics for container %s: %w", container.ID, err)
	}

	taskMetricsAny, err := typeurl.UnmarshalAny(taskMetrics.Data)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal metrics for container %s: %w", container.ID, err)
	}

	var metrics cgroupMetrics
	switch v := taskMetricsAny.(type) {
	case *cg1.Metrics:
		metrics.v1 = v
	case *cg2.Metrics:
		metrics.v2 = v
	default:
		return nil, fmt.Errorf("unexpected metrics type: %T from %s", taskMetricsAny, reflect.TypeOf(taskMetricsAny).Elem().PkgPath())
	}

	containerLabels := append(append([]string(nil), labels...), container.Config.Metadata.Name, container.ID, container.Config.Image.UserSpecifiedImage)
	timestamp := time.Now().UnixNano()

	containerMetrics := &runtime.ContainerMetrics{
		ContainerId: container.Metadata.ID,
	}

	// miscellaneous metrics
	containerMetrics.Metrics = append(containerMetrics.Metrics, []*runtime.Metric{
		{
			Name:        containerLastSeen.Name,
			Timestamp:   timestamp,
			MetricType:  runtime.MetricType_GAUGE,
			LabelValues: containerLabels,
			Value:       &runtime.UInt64Value{Value: uint64(time.Now().Unix())},
		},
		{
			Name:        containerStartTimeSeconds.Name,
			Timestamp:   timestamp,
			MetricType:  runtime.MetricType_GAUGE,
			LabelValues: containerLabels,
			Value:       &runtime.UInt64Value{Value: uint64(container.Status.Get().StartedAt)},
		},
	}...)

	cpuMetrics, err := c.extractCPUMetrics(metrics, containerLabels, timestamp)
	if err != nil {
		log.G(ctx).WithField("containerid", container.ID).WithError(err).Info("failed to extract CPU metrics")
	}
	containerMetrics.Metrics = append(containerMetrics.Metrics, cpuMetrics...)

	memoryMetrics, err := c.extractMemoryMetrics(metrics, containerLabels, timestamp)
	if err != nil {
		log.G(ctx).WithField("containerid", container.ID).WithError(err).Info("failed to extract memory metrics")
	}
	containerMetrics.Metrics = append(containerMetrics.Metrics, memoryMetrics...)

	diskMetrics, err := c.extractDiskIOMetrics(metrics, containerLabels, timestamp)
	if err != nil {
		log.G(ctx).WithField("containerid", container.ID).WithError(err).Info("failed to extract disk metrics")
	}
	containerMetrics.Metrics = append(containerMetrics.Metrics, diskMetrics...)

	fsMetrics, err := c.extractFilesystemMetrics(ctx, container, containerLabels, timestamp)
	if err != nil {
		log.G(ctx).WithField("containerid", container.ID).WithError(err).Info("failed to extract filesystem metrics")
	}
	containerMetrics.Metrics = append(containerMetrics.Metrics, fsMetrics...)

	processMetrics, err := c.extractProcessMetrics(ctx, task, metrics, containerLabels, timestamp)
	if err != nil {
		log.G(ctx).WithField("containerid", container.ID).WithError(err).Info("failed to extract process metrics")
	}
	containerMetrics.Metrics = append(containerMetrics.Metrics, processMetrics...)

	containerSpecMetrics, err := c.extractContainerSpecMetrics(ctx, task, containerLabels, timestamp)
	if err != nil {
		log.G(ctx).WithField("containerid", container.ID).WithError(err).Info("failed to extract container spec metrics")
	}
	containerMetrics.Metrics = append(containerMetrics.Metrics, containerSpecMetrics...)

	return containerMetrics, nil
}

func (c *criService) extractCPUMetrics(stats cgroupMetrics, labels []string, timestamp int64) ([]*runtime.Metric, error) {
	var metrics []*runtime.Metric

	switch {
	case stats.v1 != nil:
		s := stats.v1

		if s.CPU != nil && s.CPU.Usage != nil {
			metrics = append(metrics, &runtime.Metric{
				Name:        containerCPUUsageSecondsTotal.Name,
				Timestamp:   timestamp,
				MetricType:  runtime.MetricType_COUNTER,
				LabelValues: labels,
				Value:       &runtime.UInt64Value{Value: s.CPU.Usage.Total / uint64(time.Second)},
			})

			metrics = append(metrics, []*runtime.Metric{
				{
					Name:        containerCPUUserSecondsTotal.Name,
					Timestamp:   timestamp,
					MetricType:  runtime.MetricType_COUNTER,
					LabelValues: labels,
					Value:       &runtime.UInt64Value{Value: s.CPU.Usage.User / uint64(time.Second)},
				},
				{
					Name:        containerCPUSystemSecondsTotal.Name,
					Timestamp:   timestamp,
					MetricType:  runtime.MetricType_COUNTER,
					LabelValues: labels,
					Value:       &runtime.UInt64Value{Value: s.CPU.Usage.Kernel / uint64(time.Second)},
				},
			}...)

			if s.CPU.Throttling != nil {
				metrics = append(metrics, []*runtime.Metric{
					{
						Name:        containerCPUCfsPeriodsTotal.Name,
						Timestamp:   timestamp,
						MetricType:  runtime.MetricType_COUNTER,
						LabelValues: labels,
						Value:       &runtime.UInt64Value{Value: s.CPU.Throttling.Periods},
					},
					{
						Name:        containerCPUCfsThrottledPeriodsTotal.Name,
						Timestamp:   timestamp,
						MetricType:  runtime.MetricType_COUNTER,
						LabelValues: labels,
						Value:       &runtime.UInt64Value{Value: s.CPU.Throttling.ThrottledPeriods},
					},
					{
						Name:        containerCPUCfsThrottledSecondsTotal.Name,
						Timestamp:   timestamp,
						MetricType:  runtime.MetricType_COUNTER,
						LabelValues: labels,
						Value:       &runtime.UInt64Value{Value: s.CPU.Throttling.ThrottledTime / uint64(time.Second)},
					},
				}...)
			}
		}

	case stats.v2 != nil:
		s := stats.v2

		if s.CPU != nil {
			metrics = append(metrics, &runtime.Metric{
				Name:        containerCPUUsageSecondsTotal.Name,
				Timestamp:   timestamp,
				MetricType:  runtime.MetricType_COUNTER,
				LabelValues: labels,
				Value:       &runtime.UInt64Value{Value: s.CPU.UsageUsec * 1000 / uint64(time.Second)},
			})

			metrics = append(metrics, []*runtime.Metric{
				{
					Name:        containerCPUUserSecondsTotal.Name,
					Timestamp:   timestamp,
					MetricType:  runtime.MetricType_COUNTER,
					LabelValues: labels,
					Value:       &runtime.UInt64Value{Value: s.CPU.UserUsec * 1000 / uint64(time.Second)},
				},
				{
					Name:        containerCPUSystemSecondsTotal.Name,
					Timestamp:   timestamp,
					MetricType:  runtime.MetricType_COUNTER,
					LabelValues: labels,
					Value:       &runtime.UInt64Value{Value: s.CPU.SystemUsec * 1000 / uint64(time.Second)},
				},
			}...)

			// Always include CFS throttling metrics, even if zero
			metrics = append(metrics, []*runtime.Metric{
				{
					Name:        containerCPUCfsPeriodsTotal.Name,
					Timestamp:   timestamp,
					MetricType:  runtime.MetricType_COUNTER,
					LabelValues: labels,
					Value:       &runtime.UInt64Value{Value: s.CPU.NrPeriods},
				},
				{
					Name:        containerCPUCfsThrottledPeriodsTotal.Name,
					Timestamp:   timestamp,
					MetricType:  runtime.MetricType_COUNTER,
					LabelValues: labels,
					Value:       &runtime.UInt64Value{Value: s.CPU.NrThrottled},
				},
				{
					Name:        containerCPUCfsThrottledSecondsTotal.Name,
					Timestamp:   timestamp,
					MetricType:  runtime.MetricType_COUNTER,
					LabelValues: labels,
					Value:       &runtime.UInt64Value{Value: s.CPU.ThrottledUsec * 1000 / uint64(time.Second)},
				},
			}...)
		}
	}

	return metrics, nil
}

func (c *criService) extractMemoryMetrics(stats cgroupMetrics, labels []string, timestamp int64) ([]*runtime.Metric, error) {
	var metrics []*runtime.Metric

	switch {
	case stats.v1 != nil:
		s := stats.v1

		if s.Memory != nil {
			if s.Memory.Usage != nil {
				metrics = append(metrics, []*runtime.Metric{
					{
						Name:        containerMemoryUsageBytes.Name,
						Timestamp:   timestamp,
						MetricType:  runtime.MetricType_GAUGE,
						LabelValues: labels,
						Value:       &runtime.UInt64Value{Value: s.Memory.Usage.Usage},
					},
					{
						Name:        containerMemoryWorkingSetBytes.Name,
						Timestamp:   timestamp,
						MetricType:  runtime.MetricType_GAUGE,
						LabelValues: labels,
						Value:       &runtime.UInt64Value{Value: getWorkingSet(s.Memory)},
					},
					{
						Name:        containerMemoryMaxUsageBytes.Name,
						Timestamp:   timestamp,
						MetricType:  runtime.MetricType_GAUGE,
						LabelValues: labels,
						Value:       &runtime.UInt64Value{Value: s.Memory.Usage.Max},
					},
				}...)
			}

			metrics = append(metrics, []*runtime.Metric{
				{
					Name:        containerMemoryRss.Name,
					Timestamp:   timestamp,
					MetricType:  runtime.MetricType_GAUGE,
					LabelValues: labels,
					Value:       &runtime.UInt64Value{Value: s.Memory.TotalRSS},
				},
				{
					Name:        containerMemoryCache.Name,
					Timestamp:   timestamp,
					MetricType:  runtime.MetricType_GAUGE,
					LabelValues: labels,
					Value:       &runtime.UInt64Value{Value: s.Memory.TotalCache},
				},
				{
					Name:        containerMemoryMappedFile.Name,
					Timestamp:   timestamp,
					MetricType:  runtime.MetricType_GAUGE,
					LabelValues: labels,
					Value:       &runtime.UInt64Value{Value: s.Memory.TotalMappedFile},
				},
				{
					Name:        containerMemoryTotalActiveFileBytes.Name,
					Timestamp:   timestamp,
					MetricType:  runtime.MetricType_GAUGE,
					LabelValues: labels,
					Value:       &runtime.UInt64Value{Value: s.Memory.TotalActiveFile},
				},
				{
					Name:        containerMemoryTotalInactiveFileBytes.Name,
					Timestamp:   timestamp,
					MetricType:  runtime.MetricType_GAUGE,
					LabelValues: labels,
					Value:       &runtime.UInt64Value{Value: s.Memory.TotalInactiveFile},
				},
				{
					Name:        containerMemoryFailuresTotal.Name,
					Timestamp:   timestamp,
					MetricType:  runtime.MetricType_COUNTER,
					LabelValues: append(append([]string(nil), labels...), "pgfault", "container"),
					Value:       &runtime.UInt64Value{Value: s.Memory.PgFault},
				},
				{
					Name:        containerMemoryFailuresTotal.Name,
					Timestamp:   timestamp,
					MetricType:  runtime.MetricType_COUNTER,
					LabelValues: append(append([]string(nil), labels...), "pgmajfault", "container"),
					Value:       &runtime.UInt64Value{Value: s.Memory.PgMajFault},
				},
			}...)

			if s.Memory.Kernel != nil {
				metrics = append(metrics, &runtime.Metric{
					Name:        containerMemoryKernelUsage.Name,
					Timestamp:   timestamp,
					MetricType:  runtime.MetricType_GAUGE,
					LabelValues: labels,
					Value:       &runtime.UInt64Value{Value: s.Memory.Kernel.Usage},
				})
			}

			if s.Memory.Swap != nil {
				metrics = append(metrics, &runtime.Metric{
					Name:        containerMemorySwap.Name,
					Timestamp:   timestamp,
					MetricType:  runtime.MetricType_GAUGE,
					LabelValues: labels,
					Value:       &runtime.UInt64Value{Value: s.Memory.Swap.Usage},
				})
			}

			if s.Memory.Usage != nil {
				metrics = append(metrics, &runtime.Metric{
					Name:        containerMemoryFailcnt.Name,
					Timestamp:   timestamp,
					MetricType:  runtime.MetricType_COUNTER,
					LabelValues: labels,
					Value:       &runtime.UInt64Value{Value: s.Memory.Usage.Failcnt},
				})
			}

		}

		if s.MemoryOomControl != nil {
			metrics = append(metrics, []*runtime.Metric{
				{
					Name:        containerOomEventsTotal.Name,
					Timestamp:   timestamp,
					MetricType:  runtime.MetricType_COUNTER,
					LabelValues: labels,
					Value:       &runtime.UInt64Value{Value: s.MemoryOomControl.GetOomKill()},
				},
			}...)
		}

	case stats.v2 != nil:
		s := stats.v2

		if s.Memory != nil {
			metrics = append(metrics, []*runtime.Metric{
				{
					Name:        containerMemoryUsageBytes.Name,
					Timestamp:   timestamp,
					MetricType:  runtime.MetricType_GAUGE,
					LabelValues: labels,
					Value:       &runtime.UInt64Value{Value: s.Memory.Usage},
				},
				{
					Name:        containerMemoryMaxUsageBytes.Name,
					Timestamp:   timestamp,
					MetricType:  runtime.MetricType_GAUGE,
					LabelValues: labels,
					Value:       &runtime.UInt64Value{Value: s.Memory.UsageLimit},
				},
				{
					Name:        containerMemoryWorkingSetBytes.Name,
					Timestamp:   timestamp,
					MetricType:  runtime.MetricType_GAUGE,
					LabelValues: labels,
					Value:       &runtime.UInt64Value{Value: getWorkingSetV2(s.Memory)},
				},
				{
					Name:        containerMemoryRss.Name,
					Timestamp:   timestamp,
					MetricType:  runtime.MetricType_GAUGE,
					LabelValues: labels,
					Value:       &runtime.UInt64Value{Value: s.Memory.Anon},
				},
				{
					Name:        containerMemoryCache.Name,
					Timestamp:   timestamp,
					MetricType:  runtime.MetricType_GAUGE,
					LabelValues: labels,
					Value:       &runtime.UInt64Value{Value: s.Memory.File},
				},
				{
					Name:        containerMemoryKernelUsage.Name,
					Timestamp:   timestamp,
					MetricType:  runtime.MetricType_GAUGE,
					LabelValues: labels,
					Value:       &runtime.UInt64Value{Value: s.Memory.KernelStack},
				},
				{
					Name:        containerMemoryMappedFile.Name,
					Timestamp:   timestamp,
					MetricType:  runtime.MetricType_GAUGE,
					LabelValues: labels,
					Value:       &runtime.UInt64Value{Value: s.Memory.FileMapped},
				},
				{
					Name:        containerMemorySwap.Name,
					Timestamp:   timestamp,
					MetricType:  runtime.MetricType_GAUGE,
					LabelValues: labels,
					Value:       &runtime.UInt64Value{Value: s.Memory.SwapUsage},
				},
				{
					Name:        containerMemoryFailcnt.Name,
					Timestamp:   timestamp,
					MetricType:  runtime.MetricType_COUNTER,
					LabelValues: labels,
					Value:       &runtime.UInt64Value{Value: 0}, // cgroups v2 doesn't expose failcnt, provide 0
				},
				{
					Name:        containerMemoryTotalActiveFileBytes.Name,
					Timestamp:   timestamp,
					MetricType:  runtime.MetricType_GAUGE,
					LabelValues: labels,
					Value:       &runtime.UInt64Value{Value: s.Memory.ActiveFile},
				},
				{
					Name:        containerMemoryTotalInactiveFileBytes.Name,
					Timestamp:   timestamp,
					MetricType:  runtime.MetricType_GAUGE,
					LabelValues: labels,
					Value:       &runtime.UInt64Value{Value: s.Memory.InactiveFile},
				},
				// TODO how to get hierarchical ?
				{
					Name:        containerMemoryFailuresTotal.Name,
					Timestamp:   timestamp,
					MetricType:  runtime.MetricType_COUNTER,
					LabelValues: append(append([]string(nil), labels...), "pgfault", "container"),
					Value:       &runtime.UInt64Value{Value: s.Memory.Pgfault},
				},
				{
					Name:        containerMemoryFailuresTotal.Name,
					Timestamp:   timestamp,
					MetricType:  runtime.MetricType_COUNTER,
					LabelValues: append(append([]string(nil), labels...), "pgmajfault", "container"),
					Value:       &runtime.UInt64Value{Value: s.Memory.Pgmajfault},
				},
			}...)

		}

		if s.MemoryEvents != nil {
			metrics = append(metrics, []*runtime.Metric{
				{
					Name:        containerOomEventsTotal.Name,
					Timestamp:   timestamp,
					MetricType:  runtime.MetricType_COUNTER,
					LabelValues: labels,
					Value:       &runtime.UInt64Value{Value: s.MemoryEvents.GetOomKill()},
				},
			}...)
		}
	}

	return metrics, nil
}

func (c *criService) extractDiskIOMetrics(stats cgroupMetrics, labels []string, timestamp int64) ([]*runtime.Metric, error) {
	var metrics []*runtime.Metric

	switch {
	case stats.v1 != nil:
		s := stats.v1

		if s.Blkio != nil {
			// Process blkio device usage stats
			for _, entry := range s.Blkio.IoServiceBytesRecursive {
				metrics = append(metrics, &runtime.Metric{
					Name:        containerBlkioDeviceUsageTotal.Name,
					Timestamp:   timestamp,
					MetricType:  runtime.MetricType_COUNTER,
					LabelValues: append(append([]string(nil), labels...), entry.Device, strconv.FormatUint(entry.Major, 10), strconv.FormatUint(entry.Minor, 10), entry.Op),
					Value:       &runtime.UInt64Value{Value: entry.Value},
				})
			}

			// Process filesystem read/write stats
			for _, entry := range s.Blkio.IoServicedRecursive {
				switch strings.ToLower(entry.Op) {
				case "read":
					metrics = append(metrics, &runtime.Metric{
						Name:        containerFsReadsTotal.Name,
						Timestamp:   timestamp,
						MetricType:  runtime.MetricType_COUNTER,
						LabelValues: append(append([]string(nil), labels...), entry.Device),
						Value:       &runtime.UInt64Value{Value: entry.Value},
					})
				case "write":
					metrics = append(metrics, &runtime.Metric{
						Name:        containerFsWritesTotal.Name,
						Timestamp:   timestamp,
						MetricType:  runtime.MetricType_COUNTER,
						LabelValues: append(append([]string(nil), labels...), entry.Device),
						Value:       &runtime.UInt64Value{Value: entry.Value},
					})
				}
			}

			// Process filesystem bytes read/written
			for _, entry := range s.Blkio.IoServiceBytesRecursive {
				switch strings.ToLower(entry.Op) {
				case "read":
					metrics = append(metrics, &runtime.Metric{
						Name:        containerFsReadsBytesTotal.Name,
						Timestamp:   timestamp,
						MetricType:  runtime.MetricType_COUNTER,
						LabelValues: append(append([]string(nil), labels...), entry.Device),
						Value:       &runtime.UInt64Value{Value: entry.Value},
					})
				case "write":
					metrics = append(metrics, &runtime.Metric{
						Name:        containerFsWritesBytesTotal.Name,
						Timestamp:   timestamp,
						MetricType:  runtime.MetricType_COUNTER,
						LabelValues: append(append([]string(nil), labels...), entry.Device),
						Value:       &runtime.UInt64Value{Value: entry.Value},
					})
				}
			}
		}

	case stats.v2 != nil:
		s := stats.v2

		if s.Io != nil {
			// Process cgroups v2 I/O stats
			for _, entry := range s.Io.Usage {
				device := fmt.Sprintf("%d:%d", entry.Major, entry.Minor)
				metrics = append(metrics, []*runtime.Metric{
					{
						Name:        containerFsReadsBytesTotal.Name,
						Timestamp:   timestamp,
						MetricType:  runtime.MetricType_COUNTER,
						LabelValues: append(append([]string(nil), labels...), device),
						Value:       &runtime.UInt64Value{Value: entry.Rbytes},
					},
					{
						Name:        containerFsWritesBytesTotal.Name,
						Timestamp:   timestamp,
						MetricType:  runtime.MetricType_COUNTER,
						LabelValues: append(append([]string(nil), labels...), device),
						Value:       &runtime.UInt64Value{Value: entry.Wbytes},
					},
					{
						Name:        containerFsReadsTotal.Name,
						Timestamp:   timestamp,
						MetricType:  runtime.MetricType_COUNTER,
						LabelValues: append(append([]string(nil), labels...), device),
						Value:       &runtime.UInt64Value{Value: entry.Rios},
					},
					{
						Name:        containerFsWritesTotal.Name,
						Timestamp:   timestamp,
						MetricType:  runtime.MetricType_COUNTER,
						LabelValues: append(append([]string(nil), labels...), device),
						Value:       &runtime.UInt64Value{Value: entry.Wios},
					},
				}...)

			}
		}
	}

	return metrics, nil
}

func (c *criService) extractProcessMetrics(ctx context.Context, task containerd.Task, stats cgroupMetrics, labels []string, timestamp int64) ([]*runtime.Metric, error) {
	var metrics []*runtime.Metric

	switch {
	case stats.v1 != nil:
		s := stats.v1

		if s.Pids != nil {
			metrics = append(metrics, []*runtime.Metric{
				{
					Name:        containerProcesses.Name,
					Timestamp:   timestamp,
					MetricType:  runtime.MetricType_GAUGE,
					LabelValues: labels,
					Value:       &runtime.UInt64Value{Value: s.Pids.Current},
				},
				{
					Name:        containerThreadsMax.Name,
					Timestamp:   timestamp,
					MetricType:  runtime.MetricType_GAUGE,
					LabelValues: labels,
					Value:       &runtime.UInt64Value{Value: s.Pids.Limit},
				},
			}...)
		}

	case stats.v2 != nil:
		s := stats.v2

		if s.Pids != nil {
			metrics = append(metrics, []*runtime.Metric{
				{
					Name:        containerProcesses.Name,
					Timestamp:   timestamp,
					MetricType:  runtime.MetricType_GAUGE,
					LabelValues: labels,
					Value:       &runtime.UInt64Value{Value: s.Pids.Current},
				},
				{
					Name:        containerThreadsMax.Name,
					Timestamp:   timestamp,
					MetricType:  runtime.MetricType_GAUGE,
					LabelValues: labels,
					Value:       &runtime.UInt64Value{Value: s.Pids.Limit},
				},
				{
					Name:        containerThreads.Name,
					Timestamp:   timestamp,
					MetricType:  runtime.MetricType_GAUGE,
					LabelValues: labels,
					Value:       &runtime.UInt64Value{Value: s.Pids.Current},
				},
			}...)
		}
	}

	taskSpec, err := task.Spec(ctx)
	if err != nil {
		return metrics, fmt.Errorf("failed to get task spec: %w", err)
	}

	if taskSpec != nil && taskSpec.Process != nil {
		for _, rlimit := range taskSpec.Process.Rlimits {
			metrics = append(metrics, &runtime.Metric{
				Name:        containerUlimitsSoft.Name,
				Timestamp:   timestamp,
				MetricType:  runtime.MetricType_GAUGE,
				LabelValues: append(append([]string(nil), labels...), rlimit.Type),
				Value:       &runtime.UInt64Value{Value: rlimit.Soft},
			})
		}
	}

	// Collect file descriptor and socket metrics
	pidsInfo, err := task.Pids(ctx)
	if err != nil {
		return metrics, fmt.Errorf("failed to get container PIDs for FD/socket metrics: %w", err)
	}

	pids := make([]int, 0, len(pidsInfo))
	for _, pidInfo := range pidsInfo {
		pids = append(pids, int(pidInfo.Pid))
	}

	fdCount, socketCount, err := c.getContainerProcessDescriptorCount(ctx, pids)
	if err != nil {
		return metrics, fmt.Errorf("failed to count file descriptors and sockets: %w", err)
	}

	metrics = append(metrics, []*runtime.Metric{
		{
			Name:        containerFileDescriptors.Name,
			Timestamp:   timestamp,
			MetricType:  runtime.MetricType_GAUGE,
			LabelValues: labels,
			Value:       &runtime.UInt64Value{Value: fdCount},
		},
		{
			Name:        containerSockets.Name,
			Timestamp:   timestamp,
			MetricType:  runtime.MetricType_GAUGE,
			LabelValues: labels,
			Value:       &runtime.UInt64Value{Value: socketCount},
		},
	}...)

	return metrics, nil
}

func (c *criService) extractContainerSpecMetrics(ctx context.Context, task containerd.Task, labels []string, timestamp int64) ([]*runtime.Metric, error) {
	taskSpec, err := task.Spec(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get task spec: %w", err)
	}

	// No resource limits are set. No error but no metrics too.
	if taskSpec.Linux == nil || taskSpec.Linux.Resources == nil {
		return nil, nil
	}
	resource := taskSpec.Linux.Resources

	var metrics []*runtime.Metric
	if cpuResource := resource.CPU; cpuResource != nil {
		if cpuResource.Period != nil {
			metrics = append(metrics, &runtime.Metric{
				Name:        containerSpecCPUPeriod.Name,
				Timestamp:   timestamp,
				MetricType:  runtime.MetricType_GAUGE,
				LabelValues: labels,
				Value:       &runtime.UInt64Value{Value: *cpuResource.Period},
			})
		}
		if cpuResource.Shares != nil {
			metrics = append(metrics, &runtime.Metric{
				Name:        containerSpecCPUShares.Name,
				Timestamp:   timestamp,
				MetricType:  runtime.MetricType_GAUGE,
				LabelValues: labels,
				Value:       &runtime.UInt64Value{Value: *cpuResource.Shares},
			})
		}
	}

	if memoryResource := resource.Memory; memoryResource != nil {
		if memoryResource.Limit != nil {
			metrics = append(metrics, &runtime.Metric{
				Name:        containerSpecMemoryLimitBytes.Name,
				Timestamp:   timestamp,
				MetricType:  runtime.MetricType_GAUGE,
				LabelValues: labels,
				Value:       &runtime.UInt64Value{Value: uint64(*memoryResource.Limit)},
			})
		}
		if memoryResource.Reservation != nil {
			metrics = append(metrics, &runtime.Metric{
				Name:        containerSpecMemoryReservationLimitBytes.Name,
				Timestamp:   timestamp,
				MetricType:  runtime.MetricType_GAUGE,
				LabelValues: labels,
				Value:       &runtime.UInt64Value{Value: uint64(*memoryResource.Reservation)},
			})
		}
		if memoryResource.Swap != nil {
			metrics = append(metrics, &runtime.Metric{
				Name:        containerSpecMemorySwapLimitBytes.Name,
				Timestamp:   timestamp,
				MetricType:  runtime.MetricType_GAUGE,
				LabelValues: labels,
				Value:       &runtime.UInt64Value{Value: uint64(*memoryResource.Swap)},
			})
		}
	}
	return metrics, nil
}

// findContainerTaskRootfs tries to find the rootfs of a container,
// but it's inherently broken as this info should be provided by
// the task itself. Sadly taskSpec.Root.Path is filled with a
// relative path instead of the spec required absolute path.
// TODO: Fix this mess
func (c *criService) findContainerTaskRootfs(container containers.Container) (string, error) {
	// TODO: Can't we just read the used plugin from the container?
	var pluginPath string
	switch container.Runtime.Name {
	case "runc":
		pluginPath = string(plugins.RuntimePlugin) + ".linux"
	default:
		pluginPath = string(plugins.RuntimePluginV2) + ".task"
	}

	// For containerd, the typical bundle path structure is:
	// {stateDir/../}/{plugin}/{namespace}/{id}
	// The rootfs is typically at: {bundle}/rootfs
	bundlePath := filepath.Join(
		filepath.Join(c.config.StateDir, "../"),
		pluginPath,
		c.nri.GetName(),
		container.ID,
	)
	rootfsPath := filepath.Join(bundlePath, "rootfs")

	// Check if the path exists
	if _, err := os.Stat(rootfsPath); err == nil {
		return rootfsPath, nil
	}

	return "", fmt.Errorf("could not determine container root path")
}

func (c *criService) extractFilesystemMetrics(ctx context.Context, container containerstore.Container, labels []string, timestamp int64) ([]*runtime.Metric, error) {
	ctrInfo, err := container.Container.Info(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get container info: %w", err)
	}

	rootfs, err := c.findContainerTaskRootfs(ctrInfo)
	if err != nil {
		return nil, err
	}

	var stat syscall.Statfs_t
	if err := syscall.Statfs(rootfs, &stat); err != nil {
		return nil, fmt.Errorf("failed to get root filesystem info: %w", err)
	}

	var (
		totalBytes     = stat.Blocks * uint64(stat.Bsize)
		availableBytes = stat.Bavail * uint64(stat.Bsize)
		usedBytes      = totalBytes - availableBytes
		limit          = stat.Blocks * uint64(stat.Bsize)
		inodes         = stat.Files
		inodesFree     = stat.Ffree
	)

	snapshotUsage, err := c.client.SnapshotService(ctrInfo.Snapshotter).Usage(ctx, ctrInfo.SnapshotKey)
	if err == nil {
		usedBytes = uint64(snapshotUsage.Size)
		inodes = uint64(snapshotUsage.Inodes)
	}

	// TODO: These are wrong, but I (fionera) don't know the internals enough to correctly fetch
	// the correct device metrics.
	metrics := []*runtime.Metric{
		{
			Name:        containerFsUsageBytes.Name,
			Timestamp:   timestamp,
			MetricType:  runtime.MetricType_GAUGE,
			LabelValues: append(append([]string(nil), labels...), "/"),
			Value:       &runtime.UInt64Value{Value: usedBytes},
		},
		{
			Name:        containerFsLimitBytes.Name,
			Timestamp:   timestamp,
			MetricType:  runtime.MetricType_GAUGE,
			LabelValues: append(append([]string(nil), labels...), "/"),
			Value:       &runtime.UInt64Value{Value: limit},
		},
		{
			Name:        containerFsInodesTotal.Name,
			Timestamp:   timestamp,
			MetricType:  runtime.MetricType_GAUGE,
			LabelValues: append(append([]string(nil), labels...), "/"),
			Value:       &runtime.UInt64Value{Value: inodes},
		},
		{
			Name:        containerFsInodesFree.Name,
			Timestamp:   timestamp,
			MetricType:  runtime.MetricType_GAUGE,
			LabelValues: append(append([]string(nil), labels...), "/"),
			Value:       &runtime.UInt64Value{Value: inodesFree},
		},
	}

	return metrics, nil
}

// getContainerProcessDescriptorCount gets the total no of fds and sockets in a pid
// Referenced from https://github.com/google/cadvisor/blob/master/container/libcontainer/handler.go
func (c *criService) getContainerProcessDescriptorCount(ctx context.Context, pids []int) (fdCount, socketCount uint64, err error) {
	for _, pid := range pids {
		fdDir := path.Join("/proc", strconv.Itoa(pid), "fd")
		fds, err := os.ReadDir(fdDir)
		if err != nil {
			log.G(ctx).WithField("pid", pid).WithError(err).Debug("failed to read fd directory for PID")
			continue
		}
		fdCount += uint64(len(fds))

		for _, fd := range fds {
			fdPath := filepath.Join(fdDir, fd.Name())
			linkTarget, err := os.Readlink(fdPath)
			if err != nil {
				log.G(ctx).WithField("fd", fdPath).WithError(err).Debug("error while reading fd link")
				continue
			}

			// check if the fd is a socket
			if strings.HasPrefix(linkTarget, "socket") {
				socketCount++
			}
		}
	}

	return fdCount, socketCount, nil
}
