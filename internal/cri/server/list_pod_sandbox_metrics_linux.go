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
	"sync"

	"github.com/containerd/containerd/v2/internal/cri/util"
	"github.com/containerd/errdefs"
	"github.com/containerd/log"
	"github.com/containerd/typeurl/v2"
	"golang.org/x/time/rate"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	containerstore "github.com/containerd/containerd/v2/internal/cri/store/container"
	sandboxstore "github.com/containerd/containerd/v2/internal/cri/store/sandbox"
)

// ListPodSandboxMetrics gets pod sandbox metrics from CRI Runtime
func (c *criService) ListPodSandboxMetrics(ctx context.Context, r *runtime.ListPodSandboxMetricsRequest) (*runtime.ListPodSandboxMetricsResponse, error) {
	ctx = util.WithNamespace(ctx)
	sandboxes := c.sandboxStore.List()
	podMetrics := make([]*runtime.PodSandboxMetrics, 0, len(sandboxes))
	var mu sync.Mutex
	var wg sync.WaitGroup

	// Rate limiter to prevent overwhelming the system with concurrent requests
	limiter := rate.NewLimiter(rate.Limit(10), 10) // Allow 10 concurrent requests with burst of 10
	semaphore := make(chan struct{}, 10)           // Limit to 10 concurrent goroutines

	activeSandboxIDs := make(map[string]bool)

	for _, sandbox := range sandboxes {
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

	// Get container stats
	task, err := container.Container.Task(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get task for container %s: %w", container.ID, err)
	}

	taskMetrics, err := task.Metrics(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get metrics for container %s: %w", container.ID, err)
	}

	_, err = typeurl.UnmarshalAny(taskMetrics.Data)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal metrics for container %s: %w", container.ID, err)
	}

	containerMetrics := &runtime.ContainerMetrics{
		ContainerId: meta.ID,
		Metrics:     []*runtime.Metric{},
	}

	// TODO Collect all metrics

	return containerMetrics, nil
}
