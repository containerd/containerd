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

	cg1 "github.com/containerd/cgroups/v3/cgroup1/stats"
	cg2 "github.com/containerd/cgroups/v3/cgroup2/stats"
	"github.com/containerd/containerd/api/services/tasks/v1"
	"github.com/containerd/containerd/api/types"
	"github.com/containerd/typeurl/v2"
	"github.com/sirupsen/logrus"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func (c *criService) ListPodSandboxMetrics(ctx context.Context, r *runtime.ListPodSandboxMetricsRequest) (*runtime.ListPodSandboxMetricsResponse, error) {
	sandboxList := c.sandboxStore.List()
	//metricsList := c.sandboxStore.

	podMetrics := make([]*runtime.PodSandboxMetrics, 0)
	for _, sandbox := range sandboxList {

		containerMetrics := make([]*runtime.ContainerMetrics, 0)

		containers := c.containerStore.List()
		for _, container := range containers {
			metrics, err := c.listContainerMetrics(sandbox.ID, container.ID)
			if err != nil {
				logrus.Debug("failed to fetch metrics %v", err)
			}
			containerMetrics = append(containerMetrics, metrics)
		}

		podMetrics = append(podMetrics, &runtime.PodSandboxMetrics{
			PodSandboxId:     sandbox.ID,
			ContainerMetrics: containerMetrics,
		})
	}

	return &runtime.ListPodSandboxMetricsResponse{
		PodMetrics: podMetrics,
	}, nil
}

type containerMetrics struct {
	metrics *runtime.ContainerMetrics
}

type containerCPUMetrics struct {
	UsageUsec  uint64
	UserUsec   uint64
	SystemUsec uint64
}

// gives the metrics for a given container in a sandbox
func (c *criService) listContainerMetrics(ctx context.Context, sandboxID string, containerID string) (*runtime.ContainerMetrics, error) {
	request := &tasks.MetricsRequest{Filters: []string{"id==" + containerID}}
	resp, err := c.client.TaskService().Metrics(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch metrics for task: %w", err)
	}
	if len(resp.Metrics) != 1 {
		return nil, fmt.Errorf("unexpected metrics response: %+v", resp.Metrics)
	}
	metric, err := c.toContainerMetrics(ctx, containerID, resp.Metrics[0])
	if err != nil {
		return nil, err
	}
	return metric.metrics, nil

}

func (c *criService) toContainerMetrics(ctx context.Context, containerdID string, metrics *types.Metric) (containerMetrics, error) {
	var cm runtime.ContainerMetrics
	var metric runtime.Metric

	cm.ContainerId = containerdID

	//var pids uint64
	if metrics != nil {
		var data interface{}
		switch {
		case typeurl.Is(metrics.Data, (*cg1.Metrics)(nil)):
			data = &cg1.Metrics{}
			if err := typeurl.UnmarshalTo(metrics.Data, data); err != nil {
				return containerMetrics{}, fmt.Errorf("failed to extract container metrics: %w", err)
			}
			//pids = data.(*cg1.Metrics).GetPids().GetCurrent()
		case typeurl.Is(metrics.Data, (*cg2.Metrics)(nil)):
			data = &cg2.Metrics{}
			if err := typeurl.UnmarshalTo(metrics.Data, data); err != nil {
				return containerMetrics{}, fmt.Errorf("failed to extract container metrics: %w", err)
			}
			//pids = data.(*cg2.Metrics).GetPids().GetCurrent()
		default:
			return containerMetrics{}, fmt.Errorf("cannot convert metric data to cgroups.Metrics")
		}

		cpuMetrics, err := c.cpuMetrics(ctx, metrics)
		if err != nil {
			return containerMetrics{}, err
		}
		metric.Name = "container_cpu_usage_seconds_total"
		metric.Value = &runtime.UInt64Value{Value: cpuMetrics.UsageUsec}
		cm.Metrics = append(cm.Metrics, &metric)

	}
	return containerMetrics{metrics: &cm}, nil
}

func (c *criService) cpuMetrics(ctx context.Context, stats interface{}) (*containerCPUMetrics, error) {
	switch metrics := stats.(type) {
	case *cg1.Metrics:
		metrics.GetCPU().GetUsage()
		if metrics.CPU != nil && metrics.CPU.Usage != nil {
			return &containerCPUMetrics{
				UserUsec:   metrics.CPU.Usage.User,
				SystemUsec: metrics.CPU.Usage.Kernel,
				UsageUsec:  metrics.CPU.Usage.Total,
			}, nil
		}
	case *cg2.Metrics:
		if metrics.CPU != nil {
			return &containerCPUMetrics{
				UserUsec:   metrics.CPU.UserUsec * 1000,
				SystemUsec: metrics.CPU.SystemUsec * 1000,
				UsageUsec:  metrics.CPU.UsageUsec * 1000,
			}, nil
		}
	default:
		return nil, fmt.Errorf("unexpected metrics type: %T from %s", metrics, reflect.TypeOf(metrics).Elem().PkgPath())
	}
	return nil, nil
}
