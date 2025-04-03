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
	"time"

	cg1 "github.com/containerd/cgroups/v3/cgroup1/stats"
	cg2 "github.com/containerd/cgroups/v3/cgroup2/stats"
	"github.com/containerd/containerd/api/services/tasks/v1"
	"github.com/containerd/containerd/api/types"
	"github.com/containerd/typeurl/v2"
	"github.com/sirupsen/logrus"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

type MetricsServer struct {
	collectionPeriod time.Duration
	sandboxMetrics   map[string]*SandboxMetrics
}

type SandboxMetrics struct {
	metric *runtime.PodSandboxMetrics
}

func (m *MetricsServer) updatePodSandboxMetrics(sandboxID string) *SandboxMetrics {
	sm, ok := m.sandboxMetrics[sandboxID]
	if !ok {
		sm = &SandboxMetrics{
			metric: &runtime.PodSandboxMetrics{
				PodSandboxId:     sandboxID,
				Metrics:          []*runtime.Metric{},
				ContainerMetrics: []*runtime.ContainerMetrics{},
			},
		}
	}
}

func (c *criService) ListPodSandboxMetrics(ctx context.Context, r *runtime.ListPodSandboxMetricsRequest) (*runtime.ListPodSandboxMetricsResponse, error) {
	sandboxList := c.sandboxStore.List()
	//metricsList := c.sandboxStore.

	podMetrics := make([]*runtime.PodSandboxMetrics, 0)
	for _, sandbox := range sandboxList {

		containerMetrics := make([]*runtime.ContainerMetrics, 0)

		containers := c.containerStore.List()
		for _, container := range containers {
			metrics, err := c.listContainerMetrics(ctx, sandbox.ID, container.ID)
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
	UsageUsec          uint64
	UserUsec           uint64
	SystemUsec         uint64
	NRPeriods          uint64
	NRThrottledPeriods uint64
	ThrottledUsec      uint64
	LoadAverage10      uint64
	TasksState         uint64
}

type containerMemoryMetrics struct {
	Cache        uint64
	RSS          uint64
	Swap         uint64
	KernelUsage  uint64
	FileMapped   uint64
	FailCount    uint64
	MemoryUsage  uint64
	MaxUsage     uint64
	WorkingSet   uint64
	ActiveFile   uint64
	InactiveFile uint64
	PgFault      uint64
	PgMajFault   uint64
}

type containerNetworkMetrics struct {
	Name      string
	RxBytes   uint64
	RxPackets uint64
	RxErrors  uint64
	RxDropped uint64
	TxBytes   uint64
	TxPackets uint64
	TxErrors  uint64
	TxDropped uint64
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
