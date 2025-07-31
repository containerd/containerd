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
	"github.com/containerd/containerd/v2/internal/cri/util"
	"github.com/containerd/errdefs"
	"github.com/containerd/log"
	"github.com/containerd/typeurl/v2"
	"golang.org/x/sync/errgroup"
	"golang.org/x/time/rate"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	containerstore "github.com/containerd/containerd/v2/internal/cri/store/container"
	sandboxstore "github.com/containerd/containerd/v2/internal/cri/store/sandbox"
)

// Rate limiter to prevent overwhelming the system with concurrent requests
var limiter = rate.NewLimiter(rate.Limit(10), 10) // Allow 10 concurrent requests with burst of 10

// ListPodSandboxMetrics gets pod sandbox metrics from CRI Runtime
func (c *criService) ListPodSandboxMetrics(ctx context.Context, r *runtime.ListPodSandboxMetricsRequest) (*runtime.ListPodSandboxMetricsResponse, error) {
	ctx = util.WithNamespace(ctx)
	sandboxes := c.sandboxStore.List()
	podMetrics := make([]*runtime.PodSandboxMetrics, 0, len(sandboxes))
	var mu sync.Mutex

	// Create errgroup with context and limit concurrency to 10
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(10)

	for _, sandbox := range sandboxes {
		// Only collect metrics for ready sandboxes
		if sandbox.Status.Get().State != sandboxstore.StateReady {
			continue
		}

		// Wait for rate limiter permission
		if err := limiter.Wait(ctx); err != nil {
			log.G(ctx).WithError(err).Debug("rate limiter context cancelled")
			break
		}

		sb := sandbox
		g.Go(func() error {
			metrics, err := c.collectPodSandboxMetrics(gctx, sb)
			if err != nil {
				switch {
				case errdefs.IsUnavailable(err), errdefs.IsNotFound(err):
					log.G(gctx).WithField("podsandboxid", sb.ID).WithError(err).Debug("failed to get pod sandbox metrics, this is likely a transient error")
					// Don't return error for transient issues, just log and continue
					return nil
				case errdefs.IsCanceled(err):
					log.G(gctx).WithField("podsandboxid", sb.ID).WithError(err).Debug("metrics collection cancelled")
					// Return the cancellation error to stop other goroutines
					return err
				default:
					log.G(gctx).WithField("podsandboxid", sb.ID).WithError(err).Error("failed to collect pod sandbox metrics")
					// Don't return error for individual failures, just log and continue
					return nil
				}
			}

			mu.Lock()
			podMetrics = append(podMetrics, metrics)
			mu.Unlock()
			return nil
		})
	}

	// Wait for all goroutines to complete
	if err := g.Wait(); err != nil {
		// log the error and return the metrics that we have collected so far
		log.G(ctx).WithError(err).Error("error during metrics collection, returning partial results")
	}

	return &runtime.ListPodSandboxMetricsResponse{
		PodMetrics: podMetrics,
	}, nil
}

// collectPodSandboxMetrics collects metrics for a specific pod sandbox and its containers
func (c *criService) collectPodSandboxMetrics(ctx context.Context, sandbox sandboxstore.Sandbox) (*runtime.PodSandboxMetrics, error) {
	meta := sandbox.Metadata
	config := sandbox.Config

	// Get sandbox stats
	stats, err := metricsForSandbox(sandbox)
	if err != nil {
		return nil, fmt.Errorf("failed getting metrics for sandbox %s: %w", sandbox.ID, err)
	}

	podMetrics := &runtime.PodSandboxMetrics{
		PodSandboxId: meta.ID,
		Metrics:      []*runtime.Metric{},
	}

	// Extract pod-level labels
	podName := config.GetMetadata().GetName()
	namespace := config.GetMetadata().GetNamespace()

	if stats != nil {
		// Collect pod-level network metrics
		if sandbox.NetNSPath != "" {
			//TODO
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

// collectContainerMetrics collects metrics for a specific container
func (c *criService) collectContainerMetrics(ctx context.Context, container containerstore.Container, podName, namespace string) (*runtime.ContainerMetrics, error) {
	meta := container.Metadata
	config := container.Config

	// Get container stats
	task, err := container.Container.Task(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get task for container %s: %w", container.ID, err)
	}

	taskMetrics, err := task.Metrics(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get metrics for container %s: %w", container.ID, err)
	}

	stats, err := typeurl.UnmarshalAny(taskMetrics.Data)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal metrics for container %s: %w", container.ID, err)
	}

	containerName := config.GetMetadata().GetName()
	containerLabels := []string{containerName, podName, namespace, meta.ID}
	timestamp := time.Now().UnixNano()

	containerMetrics := &runtime.ContainerMetrics{
		ContainerId: meta.ID,
		Metrics:     []*runtime.Metric{},
	}

	// Collect CPU metrics
	cpuMetrics, err := c.extractCPUMetrics(stats, containerLabels, timestamp)
	if err != nil {
		log.G(ctx).WithField("containerid", container.ID).WithError(err).Debug("failed to extract CPU metrics")
	} else {
		containerMetrics.Metrics = append(containerMetrics.Metrics, cpuMetrics...)
	}

	return containerMetrics, nil
}

// extractCPUMetrics extracts CPU-related metrics from container stats
func (c *criService) extractCPUMetrics(stats interface{}, labels []string, timestamp int64) ([]*runtime.Metric, error) {
	var metrics []*runtime.Metric

	switch s := stats.(type) {
	case *cg1.Metrics:
		if s.CPU != nil && s.CPU.Usage != nil {
			metrics = append(metrics, &runtime.Metric{
				Name:        "container_cpu_usage_seconds_total",
				Timestamp:   timestamp,
				MetricType:  runtime.MetricType_COUNTER,
				LabelValues: labels,
				Value:       &runtime.UInt64Value{Value: s.CPU.Usage.Total / 1e9}, // Convert to seconds
			})

			metrics = append(metrics, []*runtime.Metric{
				{
					Name:        "container_cpu_user_seconds_total",
					Timestamp:   timestamp,
					MetricType:  runtime.MetricType_COUNTER,
					LabelValues: labels,
					Value:       &runtime.UInt64Value{Value: s.CPU.Usage.User / 1e9},
				},
				{
					Name:        "container_cpu_system_seconds_total",
					Timestamp:   timestamp,
					MetricType:  runtime.MetricType_COUNTER,
					LabelValues: labels,
					Value:       &runtime.UInt64Value{Value: s.CPU.Usage.Kernel / 1e9},
				},
			}...)

			if s.CPU.Throttling != nil {
				metrics = append(metrics, []*runtime.Metric{
					{
						Name:        "container_cpu_cfs_periods_total",
						Timestamp:   timestamp,
						MetricType:  runtime.MetricType_COUNTER,
						LabelValues: labels,
						Value:       &runtime.UInt64Value{Value: s.CPU.Throttling.Periods},
					},
					{
						Name:        "container_cpu_cfs_throttled_periods_total",
						Timestamp:   timestamp,
						MetricType:  runtime.MetricType_COUNTER,
						LabelValues: labels,
						Value:       &runtime.UInt64Value{Value: s.CPU.Throttling.ThrottledPeriods},
					},
					{
						Name:        "container_cpu_cfs_throttled_seconds_total",
						Timestamp:   timestamp,
						MetricType:  runtime.MetricType_COUNTER,
						LabelValues: labels,
						Value:       &runtime.UInt64Value{Value: s.CPU.Throttling.ThrottledTime / 1e9},
					},
				}...)
			}
		}

	case *cg2.Metrics:
		if s.CPU != nil {
			metrics = append(metrics, &runtime.Metric{
				Name:        "container_cpu_usage_seconds_total",
				Timestamp:   timestamp,
				MetricType:  runtime.MetricType_COUNTER,
				LabelValues: labels,
				Value:       &runtime.UInt64Value{Value: s.CPU.UsageUsec / 1e6}, // Convert microseconds to seconds
			})

			metrics = append(metrics, []*runtime.Metric{
				{
					Name:        "container_cpu_user_seconds_total",
					Timestamp:   timestamp,
					MetricType:  runtime.MetricType_COUNTER,
					LabelValues: labels,
					Value:       &runtime.UInt64Value{Value: s.CPU.UserUsec / 1e6}, // Convert microseconds to seconds
				},
				{
					Name:        "container_cpu_system_seconds_total",
					Timestamp:   timestamp,
					MetricType:  runtime.MetricType_COUNTER,
					LabelValues: labels,
					Value:       &runtime.UInt64Value{Value: s.CPU.SystemUsec / 1e6}, // Convert microseconds to seconds
				},
			}...)

			// Always include CFS throttling metrics, even if zero
			metrics = append(metrics, []*runtime.Metric{
				{
					Name:        "container_cpu_cfs_periods_total",
					Timestamp:   timestamp,
					MetricType:  runtime.MetricType_COUNTER,
					LabelValues: labels,
					Value:       &runtime.UInt64Value{Value: s.CPU.NrPeriods},
				},
				{
					Name:        "container_cpu_cfs_throttled_periods_total",
					Timestamp:   timestamp,
					MetricType:  runtime.MetricType_COUNTER,
					LabelValues: labels,
					Value:       &runtime.UInt64Value{Value: s.CPU.NrThrottled},
				},
				{
					Name:        "container_cpu_cfs_throttled_seconds_total",
					Timestamp:   timestamp,
					MetricType:  runtime.MetricType_COUNTER,
					LabelValues: labels,
					Value:       &runtime.UInt64Value{Value: s.CPU.ThrottledUsec / 1e6}, // Convert microseconds to seconds
				},
			}...)
		}

	default:
		return nil, fmt.Errorf("unexpected metrics type: %T from %s", s, reflect.TypeOf(s).Elem().PkgPath())
	}

	return metrics, nil
}
