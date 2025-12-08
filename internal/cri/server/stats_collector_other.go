//go:build !linux

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
	"github.com/containerd/containerd/api/services/tasks/v1"
	criconfig "github.com/containerd/containerd/v2/internal/cri/config"
	containerstore "github.com/containerd/containerd/v2/internal/cri/store/container"
	sandboxstore "github.com/containerd/containerd/v2/internal/cri/store/sandbox"
	"github.com/containerd/containerd/v2/internal/cri/store/stats"
)

// StatsCollector is a stub for non-Linux platforms.
// On Linux, the real implementation periodically collects CPU stats for
// containers and sandboxes, storing them in TimedStores.
type StatsCollector struct{}

// NewStatsCollector creates a new StatsCollector (stub on non-Linux).
func NewStatsCollector(config criconfig.Config) *StatsCollector {
	return &StatsCollector{}
}

// SetDependencies sets the dependencies needed for stats collection (no-op on non-Linux).
func (c *StatsCollector) SetDependencies(
	taskService tasks.TasksClient,
	listContainers func() []containerstore.Container,
	listSandboxes func() []sandboxstore.Sandbox,
) {
}

// Start begins the background stats collection loop (no-op on non-Linux).
func (c *StatsCollector) Start() {}

// Stop stops the background stats collection loop (no-op on non-Linux).
func (c *StatsCollector) Stop() {}

// GetUsageNanoCores returns the latest calculated UsageNanoCores for the given
// container/sandbox ID. Returns 0 and false on non-Linux platforms.
func (c *StatsCollector) GetUsageNanoCores(id string) (uint64, bool) {
	return 0, false
}

// GetLatestSample returns the latest CPU sample for the given container/sandbox ID.
// Returns nil on non-Linux platforms.
func (c *StatsCollector) GetLatestSample(id string) *stats.CPUSample {
	return nil
}

// AddContainer creates a stats store for a container/sandbox (no-op on non-Linux).
func (c *StatsCollector) AddContainer(id string) {}

// RemoveContainer removes the stats store for a container/sandbox (no-op on non-Linux).
func (c *StatsCollector) RemoveContainer(id string) {}
