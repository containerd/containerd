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
	criconfig "github.com/containerd/containerd/v2/internal/cri/config"
	containerstore "github.com/containerd/containerd/v2/internal/cri/store/container"
	sandboxstore "github.com/containerd/containerd/v2/internal/cri/store/sandbox"
	"github.com/containerd/containerd/v2/internal/cri/store/stats"
	ctrdutil "github.com/containerd/containerd/v2/internal/cri/util"
)

const (
	// defaultCollectionInterval is how often the collector fetches metrics.
	// This matches cAdvisor's default housekeeping interval.
	defaultCollectionInterval = 1 * time.Second

	// defaultStatsAge is how long stats samples are retained.
	defaultStatsAge = 2 * time.Minute
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
	// doneCh signals when the collection loop has stopped
	doneCh chan struct{}
	// stopOnce ensures Stop() only closes stopCh once
	stopOnce sync.Once
	// taskService provides access to task metrics
	taskService tasks.TasksClient
	// listContainers returns the list of containers
	listContainers func() []containerstore.Container
	// listSandboxes returns the list of sandboxes
	listSandboxes func() []sandboxstore.Sandbox
}

// NewStatsCollector creates a new StatsCollector.
// The service must be set via SetService before calling Start.
func NewStatsCollector(config criconfig.Config) *StatsCollector {
	interval := defaultCollectionInterval
	statsAge := defaultStatsAge

	// Use config values if provided
	if config.StatsCollectPeriod != "" {
		if d, err := time.ParseDuration(config.StatsCollectPeriod); err == nil {
			interval = d
		}
	}
	if config.StatsRetentionPeriod != "" {
		if d, err := time.ParseDuration(config.StatsRetentionPeriod); err == nil {
			statsAge = d
		}
	}

	// Calculate maxSamples from statsAge and interval
	maxSamples := int(statsAge / interval)
	if maxSamples < 2 {
		maxSamples = 2 // Need at least 2 samples to calculate rate
	}

	return &StatsCollector{
		stores:     make(map[string]*stats.TimedStore),
		interval:   interval,
		statsAge:   statsAge,
		maxSamples: maxSamples,
		stopCh:     make(chan struct{}),
		doneCh:     make(chan struct{}),
	}
}

// SetDependencies sets the dependencies needed for stats collection.
// Must be called before Start.
func (c *StatsCollector) SetDependencies(
	taskService tasks.TasksClient,
	listContainers func() []containerstore.Container,
	listSandboxes func() []sandboxstore.Sandbox,
) {
	c.taskService = taskService
	c.listContainers = listContainers
	c.listSandboxes = listSandboxes
}

// Start begins the background stats collection loop.
func (c *StatsCollector) Start() {
	go c.collectLoop()
}

// Stop stops the background stats collection loop and waits for it to finish.
// It is safe to call Stop multiple times.
func (c *StatsCollector) Stop() {
	c.stopOnce.Do(func() {
		close(c.stopCh)
	})
	<-c.doneCh
}

// collectLoop periodically collects stats for all containers and sandboxes.
func (c *StatsCollector) collectLoop() {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	defer close(c.doneCh)

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
	containers := c.listContainers()
	if len(containers) == 0 {
		return
	}

	// Build request for all containers
	req := &tasks.MetricsRequest{}
	for _, cntr := range containers {
		req.Filters = append(req.Filters, "id=="+cntr.ID)
	}

	resp, err := c.taskService.Metrics(ctx, req)
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
	sandboxes := c.listSandboxes()
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
// If a store doesn't exist for the ID, one is created (fallback for containers
// that existed before the collector started, e.g., after a restart).
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

// AddContainer creates a stats store for a container/sandbox.
// Should be called when a container or sandbox is added to the store.
func (c *StatsCollector) AddContainer(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.stores[id]; !ok {
		c.stores[id] = stats.NewTimedStore(c.statsAge, c.maxSamples)
	}
}

// RemoveContainer removes the stats store for a container/sandbox.
// Should be called when a container or sandbox is removed from the store.
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
