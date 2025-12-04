//go:build linux

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
	"sync"
	"time"

	"github.com/containerd/cgroups/v3"
	"github.com/containerd/cgroups/v3/cgroup1"
	cg1 "github.com/containerd/cgroups/v3/cgroup1/stats"
	cgroupsv2 "github.com/containerd/cgroups/v3/cgroup2"
	cg2 "github.com/containerd/cgroups/v3/cgroup2/stats"
	"github.com/containerd/log"
	"github.com/containerd/typeurl/v2"

	"github.com/containerd/containerd/api/services/tasks/v1"
	"github.com/containerd/containerd/v2/internal/cri/store/stats"
	ctrdutil "github.com/containerd/containerd/v2/internal/cri/util"
)

const (
	// defaultCollectionInterval is how often the collector fetches metrics.
	// This matches cAdvisor's default housekeeping interval.
	defaultCollectionInterval = 1 * time.Second

	// defaultStatsAge is how long stats samples are retained.
	defaultStatsAge = 2 * time.Minute

	// defaultMaxSamples is the maximum number of samples to keep per container.
	// 120 samples at 1s interval = 2 minutes of history
	defaultMaxSamples = 120
)

// StatsCollector periodically collects CPU stats for containers and sandboxes,
// storing them in TimedStores. This allows UsageNanoCores to be calculated
// from historical samples, similar to how cAdvisor works.
type StatsCollector struct {
	mu sync.RWMutex
	// stores maps container/sandbox ID to their TimedStore
	stores map[string]*stats.TimedStore
	// interval is how often to collect stats
	interval time.Duration
	// statsAge is how long to keep samples
	statsAge time.Duration
	// maxSamples is the max number of samples per container
	maxSamples int
	// stopCh is used to stop the collection loop
	stopCh chan struct{}
	// service provides access to task metrics
	service *criService
}

// NewStatsCollector creates a new StatsCollector.
func NewStatsCollector(service *criService) *StatsCollector {
	return &StatsCollector{
		stores:     make(map[string]*stats.TimedStore),
		interval:   defaultCollectionInterval,
		statsAge:   defaultStatsAge,
		maxSamples: defaultMaxSamples,
		stopCh:     make(chan struct{}),
		service:    service,
	}
}

// Start begins the background stats collection loop.
func (c *StatsCollector) Start() {
	go c.collectLoop()
}

// Stop stops the background stats collection loop.
func (c *StatsCollector) Stop() {
	close(c.stopCh)
}

// collectLoop periodically collects stats for all containers and sandboxes.
func (c *StatsCollector) collectLoop() {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	// Do an initial collection immediately
	c.collect()

	for {
		select {
		case <-c.stopCh:
			return
		case <-ticker.C:
			c.collect()
		}
	}
}

// collect fetches metrics for all containers and sandboxes and stores them.
func (c *StatsCollector) collect() {
	// Use namespaced context to access containers in the k8s.io namespace
	ctx := ctrdutil.NamespacedContext()

	// Collect container stats
	c.collectContainerStats(ctx)

	// Collect sandbox stats (Linux only, uses cgroup metrics)
	c.collectSandboxStats(ctx)
}

// collectContainerStats fetches metrics for all containers.
func (c *StatsCollector) collectContainerStats(ctx context.Context) {
	containers := c.service.containerStore.List()
	if len(containers) == 0 {
		return
	}

	// Build request for all containers
	req := &tasks.MetricsRequest{}
	for _, cntr := range containers {
		req.Filters = append(req.Filters, "id=="+cntr.ID)
	}

	resp, err := c.service.client.TaskService().Metrics(ctx, req)
	if err != nil {
		log.G(ctx).WithError(err).Debug("StatsCollector: failed to fetch container metrics")
		return
	}

	timestamp := time.Now()
	for _, metric := range resp.Metrics {
		usageCoreNanoSeconds, ok := extractCPUUsage(metric.Data)
		if !ok {
			continue
		}
		c.addSample(metric.ID, timestamp, usageCoreNanoSeconds)
	}
}

// collectSandboxStats fetches metrics for all sandboxes.
// On Linux, sandbox/pod stats come from the parent cgroup (which includes all
// containers in the pod), not from the pause container's task metrics.
// This matches how sandbox_stats_linux.go retrieves pod-level CPU stats.
func (c *StatsCollector) collectSandboxStats(ctx context.Context) {
	sandboxes := c.service.sandboxStore.List()
	if len(sandboxes) == 0 {
		return
	}

	timestamp := time.Now()
	for _, sb := range sandboxes {
		// Get the parent cgroup path for the pod
		cgroupPath := sb.Config.GetLinux().GetCgroupParent()
		if cgroupPath == "" {
			continue
		}

		usageCoreNanoSeconds, ok := c.getCgroupCPUUsage(ctx, cgroupPath)
		if !ok {
			continue
		}
		c.addSample(sb.ID, timestamp, usageCoreNanoSeconds)
	}
}

// getCgroupCPUUsage reads CPU usage from a cgroup path.
// Supports both cgroupv1 and cgroupv2.
func (c *StatsCollector) getCgroupCPUUsage(ctx context.Context, cgroupPath string) (uint64, bool) {
	switch cgroups.Mode() {
	case cgroups.Unified:
		cg, err := cgroupsv2.Load(cgroupPath)
		if err != nil {
			log.G(ctx).WithError(err).Debugf("StatsCollector: failed to load cgroupv2: %s", cgroupPath)
			return 0, false
		}
		stats, err := cg.Stat()
		if err != nil {
			log.G(ctx).WithError(err).Debugf("StatsCollector: failed to get cgroupv2 stats: %s", cgroupPath)
			return 0, false
		}
		if stats.CPU != nil {
			// cgroupv2 reports in microseconds, convert to nanoseconds
			return stats.CPU.UsageUsec * 1000, true
		}
	default:
		control, err := cgroup1.Load(cgroup1.StaticPath(cgroupPath))
		if err != nil {
			log.G(ctx).WithError(err).Debugf("StatsCollector: failed to load cgroupv1: %s", cgroupPath)
			return 0, false
		}
		stats, err := control.Stat(cgroup1.IgnoreNotExist)
		if err != nil {
			log.G(ctx).WithError(err).Debugf("StatsCollector: failed to get cgroupv1 stats: %s", cgroupPath)
			return 0, false
		}
		if stats.CPU != nil && stats.CPU.Usage != nil {
			return stats.CPU.Usage.Total, true
		}
	}
	return 0, false
}

// addSample adds a CPU sample for the given container/sandbox ID.
func (c *StatsCollector) addSample(id string, timestamp time.Time, usageCoreNanoSeconds uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	store, ok := c.stores[id]
	if !ok {
		store = stats.NewTimedStore(c.statsAge, c.maxSamples)
		c.stores[id] = store
	}
	store.Add(timestamp, usageCoreNanoSeconds)
}

// GetUsageNanoCores returns the latest calculated UsageNanoCores for the given
// container/sandbox ID. Returns 0 and false if no data is available or if
// there aren't enough samples to calculate the rate.
func (c *StatsCollector) GetUsageNanoCores(id string) (uint64, bool) {
	c.mu.RLock()
	store, ok := c.stores[id]
	c.mu.RUnlock()

	if !ok {
		return 0, false
	}

	return store.GetLatestUsageNanoCores()
}

// GetLatestSample returns the latest CPU sample for the given container/sandbox ID.
// Returns nil if no data is available.
func (c *StatsCollector) GetLatestSample(id string) *stats.CPUSample {
	c.mu.RLock()
	store, ok := c.stores[id]
	c.mu.RUnlock()

	if !ok {
		return nil
	}

	return store.GetLatest()
}

// RemoveContainer removes the stats store for a container/sandbox.
// Should be called when a container is removed.
func (c *StatsCollector) RemoveContainer(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.stores, id)
}

// extractCPUUsage extracts the CPU usage in nanoseconds from the metric data.
// Supports both cgroupv1 and cgroupv2 metrics.
func extractCPUUsage(data typeurl.Any) (uint64, bool) {
	if data == nil {
		return 0, false
	}

	taskMetrics, err := typeurl.UnmarshalAny(data)
	if err != nil {
		return 0, false
	}

	switch v := taskMetrics.(type) {
	case *cg1.Metrics:
		if v.CPU != nil && v.CPU.Usage != nil {
			return v.CPU.Usage.Total, true
		}
	case *cg2.Metrics:
		if v.CPU != nil {
			// cgroupv2 reports in microseconds, convert to nanoseconds
			return v.CPU.UsageUsec * 1000, true
		}
	}

	return 0, false
}
