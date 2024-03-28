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
	"testing"
	"time"

	wstats "github.com/Microsoft/hcsshim/cmd/containerd-shim-runhcs-v1/stats"
	containerstore "github.com/containerd/containerd/v2/internal/cri/store/container"
	sandboxstore "github.com/containerd/containerd/v2/internal/cri/store/sandbox"
	"github.com/containerd/containerd/v2/internal/cri/store/stats"
	"github.com/containerd/containerd/v2/pkg/protobuf"
	"github.com/stretchr/testify/assert"
)

func Test_criService_podSandboxStats(t *testing.T) {
	initialStatsTimestamp := time.Now()
	currentStatsTimestamp := initialStatsTimestamp.Add(time.Second)

	c := newTestCRIService()

	type expectedStats struct {
		UsageCoreNanoSeconds uint64
		UsageNanoCores       uint64
		WorkingSetBytes      uint64
		CommitMemoryBytes    uint64
	}
	for _, test := range []struct {
		desc                   string
		metrics                map[string]*wstats.Statistics
		sandbox                sandboxstore.Sandbox
		containers             []containerstore.Container
		expectedPodStats       *expectedStats
		expectedContainerStats []expectedStats
		expectError            bool
	}{
		{
			desc:                   "no metrics found should return error",
			metrics:                map[string]*wstats.Statistics{},
			sandbox:                sandboxstore.Sandbox{},
			containers:             []containerstore.Container{},
			expectedPodStats:       &expectedStats{},
			expectedContainerStats: []expectedStats{},
			expectError:            true,
		},
		{
			desc: "pod stats will include the container stats",
			metrics: map[string]*wstats.Statistics{
				"c1": {
					Container: windowsStat(currentStatsTimestamp, 200, 20, 20),
				},
				"s1": {
					Container: windowsStat(currentStatsTimestamp, 200, 20, 20),
				},
			},
			sandbox: sandboxstore.Sandbox{Metadata: sandboxstore.Metadata{ID: "s1"}},
			containers: []containerstore.Container{
				newContainer("c1", running, nil),
			},
			expectedPodStats: &expectedStats{
				UsageCoreNanoSeconds: 400,
				UsageNanoCores:       0,
				WorkingSetBytes:      40,
				CommitMemoryBytes:    40,
			},
			expectedContainerStats: []expectedStats{
				{
					UsageCoreNanoSeconds: 200,
					UsageNanoCores:       0,
					WorkingSetBytes:      20,
					CommitMemoryBytes:    20,
				},
			},
			expectError: false,
		},
		{
			desc: "pod stats will include the init container stats",
			metrics: map[string]*wstats.Statistics{
				"c1": {
					Container: windowsStat(currentStatsTimestamp, 200, 20, 20),
				},
				"s1": {
					Container: windowsStat(currentStatsTimestamp, 200, 20, 20),
				},
				"i1": {
					Container: windowsStat(currentStatsTimestamp, 200, 20, 20),
				},
			},
			sandbox: sandboxstore.Sandbox{Metadata: sandboxstore.Metadata{ID: "s1"}},
			containers: []containerstore.Container{
				newContainer("c1", running, nil),
				newContainer("i1", running, nil),
			},
			expectedPodStats: &expectedStats{
				UsageCoreNanoSeconds: 600,
				UsageNanoCores:       0,
				WorkingSetBytes:      60,
				CommitMemoryBytes:    60,
			},
			expectedContainerStats: []expectedStats{
				{
					UsageCoreNanoSeconds: 200,
					UsageNanoCores:       0,
					WorkingSetBytes:      20,
					CommitMemoryBytes:    20,
				},
				{
					UsageCoreNanoSeconds: 200,
					UsageNanoCores:       0,
					WorkingSetBytes:      20,
					CommitMemoryBytes:    20,
				},
			},
			expectError: false,
		},
		{
			desc: "pod stats will not include the init container stats if it is stopped",
			metrics: map[string]*wstats.Statistics{
				"c1": {
					Container: windowsStat(currentStatsTimestamp, 200, 20, 20),
				},
				"s1": {
					Container: windowsStat(currentStatsTimestamp, 200, 20, 20),
				},
			},
			sandbox: sandboxstore.Sandbox{Metadata: sandboxstore.Metadata{ID: "s1"}},
			containers: []containerstore.Container{
				newContainer("c1", running, nil),
				newContainer("i1", exitedValid, nil),
			},
			expectedPodStats: &expectedStats{
				UsageCoreNanoSeconds: 400,
				UsageNanoCores:       0,
				WorkingSetBytes:      40,
				CommitMemoryBytes:    40,
			},
			expectedContainerStats: []expectedStats{
				{
					UsageCoreNanoSeconds: 200,
					UsageNanoCores:       0,
					WorkingSetBytes:      20,
					CommitMemoryBytes:    20,
				},
			},
			expectError: false,
		},
		{
			desc: "pod stats will not include the init container stats if it is stopped in failed state",
			metrics: map[string]*wstats.Statistics{
				"c1": {
					Container: windowsStat(currentStatsTimestamp, 200, 20, 20),
				},
				"s1": {
					Container: windowsStat(currentStatsTimestamp, 200, 20, 20),
				},
			},
			sandbox: sandboxstore.Sandbox{Metadata: sandboxstore.Metadata{ID: "s1"}},
			containers: []containerstore.Container{
				newContainer("c1", running, nil),
				newContainer("i1", exitedInvalid, nil),
			},
			expectedPodStats: &expectedStats{
				UsageCoreNanoSeconds: 400,
				UsageNanoCores:       0,
				WorkingSetBytes:      40,
				CommitMemoryBytes:    40,
			},
			expectedContainerStats: []expectedStats{
				{
					UsageCoreNanoSeconds: 200,
					UsageNanoCores:       0,
					WorkingSetBytes:      20,
					CommitMemoryBytes:    20,
				},
			},
			expectError: false,
		},
		{
			desc: "pod with existing stats will have usagenanocores totalled across pods and containers",
			metrics: map[string]*wstats.Statistics{
				"c1": {
					Container: windowsStat(currentStatsTimestamp, 400, 20, 20),
				},
				"s1": {
					Container: windowsStat(currentStatsTimestamp, 400, 20, 20),
				},
			},
			sandbox: sandboxPod("s1", initialStatsTimestamp, 400, 200),
			containers: []containerstore.Container{
				newContainer("c1", running, &stats.ContainerStats{
					Timestamp:            initialStatsTimestamp,
					UsageCoreNanoSeconds: 200,
					UsageNanoCores:       200,
				}),
			},
			expectedPodStats: &expectedStats{
				UsageCoreNanoSeconds: 800,
				UsageNanoCores:       400,
				WorkingSetBytes:      40,
				CommitMemoryBytes:    40,
			},
			expectedContainerStats: []expectedStats{
				{
					UsageCoreNanoSeconds: 400,
					UsageNanoCores:       200,
					WorkingSetBytes:      20,
					CommitMemoryBytes:    20,
				},
			},
			expectError: false,
		},
		{
			desc: "pod sandbox with nil stats still works (hostprocess container scenario)",
			metrics: map[string]*wstats.Statistics{
				"c1": {
					Container: windowsStat(currentStatsTimestamp, 400, 20, 20),
				},
				"s1": nil,
			},
			sandbox: sandboxPod("s1", initialStatsTimestamp, 200, 0),
			containers: []containerstore.Container{
				newContainer("c1", running, &stats.ContainerStats{
					Timestamp:            initialStatsTimestamp,
					UsageCoreNanoSeconds: 200,
					UsageNanoCores:       200,
				}),
			},
			expectedPodStats: &expectedStats{
				UsageCoreNanoSeconds: 400,
				UsageNanoCores:       200,
				WorkingSetBytes:      20,
				CommitMemoryBytes:    20,
			},
			expectedContainerStats: []expectedStats{
				{
					UsageCoreNanoSeconds: 400,
					UsageNanoCores:       200,
					WorkingSetBytes:      20,
					CommitMemoryBytes:    20,
				},
			},
			expectError: false,
		},
		{
			desc: "pod sandbox with empty stats still works (hostprocess container scenario)",
			metrics: map[string]*wstats.Statistics{
				"c1": {
					Container: windowsStat(currentStatsTimestamp, 400, 20, 20),
				},
				"s1": {},
			},
			sandbox: sandboxPod("s1", initialStatsTimestamp, 200, 0),
			containers: []containerstore.Container{
				newContainer("c1", running, &stats.ContainerStats{
					Timestamp:            initialStatsTimestamp,
					UsageCoreNanoSeconds: 200,
					UsageNanoCores:       200,
				}),
			},
			expectedPodStats: &expectedStats{
				UsageCoreNanoSeconds: 400,
				UsageNanoCores:       200,
				WorkingSetBytes:      20,
				CommitMemoryBytes:    20,
			},
			expectedContainerStats: []expectedStats{
				{
					UsageCoreNanoSeconds: 400,
					UsageNanoCores:       200,
					WorkingSetBytes:      20,
					CommitMemoryBytes:    20,
				},
			},
			expectError: false,
		},
		{
			desc: "pod sandbox with a container that has no cpu shouldn't error",
			metrics: map[string]*wstats.Statistics{
				"c1": {},
				"s1": {},
			},
			sandbox: sandboxPod("s1", initialStatsTimestamp, 200, 0),
			containers: []containerstore.Container{
				newContainer("c1", running, &stats.ContainerStats{
					Timestamp:            initialStatsTimestamp,
					UsageCoreNanoSeconds: 200,
				}),
			},
			expectedPodStats:       nil,
			expectedContainerStats: []expectedStats{},
			expectError:            false,
		},
		{
			desc:                   "pod sandbox with no stats in metric mapp will fail",
			metrics:                map[string]*wstats.Statistics{},
			sandbox:                sandboxPod("s1", initialStatsTimestamp, 200, 0),
			containers:             []containerstore.Container{},
			expectedPodStats:       nil,
			expectedContainerStats: []expectedStats{},
			expectError:            true,
		},
	} {
		test := test
		t.Run(test.desc, func(t *testing.T) {
			actualPodStats, actualContainerStats, err := c.toPodSandboxStats(test.sandbox, test.metrics, test.containers, currentStatsTimestamp)
			if test.expectError {
				assert.NotNil(t, err)
				return
			}
			assert.Nil(t, err)

			if test.expectedPodStats == nil {
				assert.Nil(t, actualPodStats.Cpu)
				assert.Nil(t, actualPodStats.Memory)
				return
			}

			assert.Equal(t, test.expectedPodStats.UsageCoreNanoSeconds, actualPodStats.Cpu.UsageCoreNanoSeconds.Value)
			assert.Equal(t, int(test.expectedPodStats.UsageNanoCores), int(actualPodStats.Cpu.UsageNanoCores.Value))

			for i, expectedStat := range test.expectedContainerStats {
				actutalStat := actualContainerStats[i]

				assert.Equal(t, expectedStat.UsageCoreNanoSeconds, actutalStat.Cpu.UsageCoreNanoSeconds.Value)
				assert.Equal(t, expectedStat.UsageNanoCores, actutalStat.Cpu.UsageNanoCores.Value)
			}
		})
	}
}

func sandboxPod(id string, timestamp time.Time, cachedCPU uint64, cachedNanoCores uint64) sandboxstore.Sandbox {
	return sandboxstore.Sandbox{
		Metadata: sandboxstore.Metadata{ID: id, RuntimeHandler: "runc"},
		Stats: &stats.ContainerStats{
			Timestamp:            timestamp,
			UsageCoreNanoSeconds: cachedCPU,
			UsageNanoCores:       cachedNanoCores,
		}}
}

func windowsStat(timestamp time.Time, cpu uint64, memory uint64, commitMemory uint64) *wstats.Statistics_Windows {
	return &wstats.Statistics_Windows{
		Windows: &wstats.WindowsContainerStatistics{
			Timestamp: protobuf.ToTimestamp(timestamp),
			Processor: &wstats.WindowsContainerProcessorStatistics{
				TotalRuntimeNS: cpu,
			},
			Memory: &wstats.WindowsContainerMemoryStatistics{
				MemoryUsagePrivateWorkingSetBytes: memory,
				MemoryUsageCommitBytes:            commitMemory,
			},
		},
	}
}

func newContainer(id string, status containerstore.Status, stats *stats.ContainerStats) containerstore.Container {
	cntr, err := containerstore.NewContainer(containerstore.Metadata{ID: id}, containerstore.WithFakeStatus(status))
	if err != nil {
		panic(err)
	}
	if stats != nil {
		cntr.Stats = stats
	}
	return cntr
}

var exitedValid = containerstore.Status{
	StartedAt:  time.Now().UnixNano(),
	FinishedAt: time.Now().UnixNano(),
	ExitCode:   0,
}

var exitedInvalid = containerstore.Status{
	StartedAt:  time.Now().UnixNano(),
	FinishedAt: time.Now().UnixNano(),
	ExitCode:   1,
}

var running = containerstore.Status{
	StartedAt: time.Now().UnixNano(),
}
