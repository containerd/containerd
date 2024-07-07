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

	wstats "github.com/Microsoft/hcsshim/cmd/containerd-shim-runhcs-v1/stats"
	cg1 "github.com/containerd/cgroups/v3/cgroup1/stats"
	cg2 "github.com/containerd/cgroups/v3/cgroup2/stats"
	"github.com/containerd/containerd/api/services/tasks/v1"
	ctrdutil "github.com/containerd/containerd/v2/internal/cri/util"
	"github.com/containerd/containerd/v2/pkg/protobuf"

	"github.com/containerd/containerd/api/types"
	"github.com/containerd/containerd/v2/internal/cri/store/stats"
	"github.com/containerd/log"
)

// MetricMonitor is used to monitor stat.
type metricMonitor struct {
	c                *criService
	collectionPeriod time.Duration
}

// newMetricMonitor creates a new monitor which will collect stats for containers and sandboxes
// every 10 seconds and calculate the NanoCores Usage
func newMetricMonitor(c *criService) *metricMonitor {
	return &metricMonitor{
		c:                c,
		collectionPeriod: time.Duration(c.config.StatsMetricsPeriod) * time.Second,
	}
}

func (m *metricMonitor) Start() {
	go func() {
		log.L.Info("Starting stat sync")
		for {
			err := m.collect()
			if err != nil {
				log.L.Warnf("Failed to collect stat: %v", err)
			}
			time.Sleep(m.collectionPeriod)
		}
	}()
}

func (m *metricMonitor) collect() error {
	// split building the request into two parts
	// get all the containers and sandboxes
	req, sandboxes, containers := m.buildMetricsRequest()
	if len(sandboxes) == 0 && len(containers) == 0 {
		log.L.Debugf("No containers or sandboxes to collect stats for")
		return nil
	}

	resp, err := m.c.client.TaskService().Metrics(ctrdutil.WithNamespace(context.Background()), req)
	if err != nil {
		return fmt.Errorf("failed to get stat: %v", err)
	}
	for _, stat := range resp.Metrics {
		err := m.processMetric(stat, sandboxes)
		if err != nil {
			log.L.Debugf("Failed to process metric: %v", err)
			continue
		}
	}

	return nil
}

func (m *metricMonitor) processMetric(stat *types.Metric, sandboxes map[string]struct{}) error {
	if stat == nil {
		return fmt.Errorf("empty metric")
	}

	oldStats, saveStatsFunc, err := m.getExistingStats(stat, sandboxes)
	if err != nil {
		return fmt.Errorf("failed to find container or sandbox: %v", err)
	}

	data, err := m.extractStats(sandboxes, stat)
	if err != nil {
		return fmt.Errorf("failed to extract stats: %v", err)
	}

	var currentCPU uint64
	var currentTimestamp = protobuf.FromTimestamp(stat.Timestamp)
	switch m := data.(type) {
	case *cg1.Metrics:
		if m.CPU == nil || m.CPU.Usage == nil {
			return fmt.Errorf("missing CPU data")
		}
		currentCPU = m.CPU.Usage.Total
	case *cg2.Metrics:
		if m.CPU == nil {
			return fmt.Errorf("missing CPU data")
		}
		// convert to nano seconds
		currentCPU = m.CPU.UsageUsec * 1000
	case *wstats.Statistics:
		wstats := data.(*wstats.Statistics).GetWindows()
		if _, ok := sandboxes[stat.ID]; ok && wstats == nil {
			// hostprocess containers sandboxes don't have stats
			currentTimestamp = protobuf.FromTimestamp(stat.Timestamp)
			currentCPU = 0
		} else if wstats != nil && wstats.Processor != nil {
			// this is slightly more accurate as it came from the windows shim
			currentTimestamp = protobuf.FromTimestamp(wstats.Timestamp)
			currentCPU = wstats.Processor.TotalRuntimeNS
		} else {
			return fmt.Errorf("invalid Windows data")
		}
	default:
		return fmt.Errorf("unknown metric type")
	}

	nanoCores := calculateNanoCores(currentCPU, oldStats, currentTimestamp)
	newStats := &stats.ContainerStats{
		UsageCoreNanoSeconds: currentCPU,
		UsageNanoCores:       nanoCores,
		Timestamp:            currentTimestamp,
	}
	err = saveStatsFunc(stat.ID, newStats)
	if err != nil {
		return fmt.Errorf("failed to save container stats: %v", err)
	}

	return nil
}

func (m *metricMonitor) buildMetricsRequest() (*tasks.MetricsRequest, map[string]struct{}, map[string]struct{}) {
	req := &tasks.MetricsRequest{}
	sandboxes := map[string]struct{}{}
	containers := map[string]struct{}{}
	for _, sandbox := range m.c.sandboxStore.List() {
		sandboxes[sandbox.ID] = struct{}{}
		req.Filters = append(req.Filters, "id=="+sandbox.ID)
	}

	for _, cntr := range m.c.containerStore.List() {
		containers[cntr.ID] = struct{}{}
		req.Filters = append(req.Filters, "id=="+cntr.ID)
	}
	return req, sandboxes, containers
}

type saveContainerStats func(id string, containerStats *stats.ContainerStats) error

func (m *metricMonitor) getExistingStats(stat *types.Metric, sandboxes map[string]struct{}) (*stats.ContainerStats, saveContainerStats, error) {
	// it was seen as a sandbox
	if _, ok := sandboxes[stat.ID]; ok {
		sandbox, err := m.c.sandboxStore.Get(stat.ID)
		if err == nil {
			saveStatsFunc := m.c.sandboxStore.UpdateContainerStats
			return sandbox.Stats, saveStatsFunc, err
		}
	}

	cntr, err := m.c.containerStore.Get(stat.ID)
	if err == nil {
		saveStatsFunc := m.c.containerStore.UpdateContainerStats
		return cntr.Stats, saveStatsFunc, err
	}

	// means we can't find it in either store
	return nil, nil, fmt.Errorf("container not found: %s", stat.ID)
}

func calculateNanoCores(usageCoreNanoSeconds uint64, oldStats *stats.ContainerStats, newtimestamp time.Time) uint64 {
	if oldStats == nil {
		return 0
	}

	nanoSeconds := newtimestamp.UnixNano() - oldStats.Timestamp.UnixNano()

	// zero or negative interval
	if nanoSeconds <= 0 {
		return 0
	}

	return uint64(float64(usageCoreNanoSeconds-oldStats.UsageCoreNanoSeconds) /
		float64(nanoSeconds) * float64(time.Second/time.Nanosecond))
}
