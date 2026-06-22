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
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestGetUsageNanoCores(t *testing.T) {
	timestamp := time.Now()
	secondAfterTimeStamp := timestamp.Add(time.Second)
	ID := "ID"

	for _, test := range []struct {
		desc                        string
		firstCPUValue               uint64
		secondCPUValue              uint64
		expectedNanoCoreUsageFirst  uint64
		expectedNanoCoreUsageSecond uint64
	}{
		{
			desc:                        "metrics",
			firstCPUValue:               50,
			secondCPUValue:              500,
			expectedNanoCoreUsageFirst:  0,
			expectedNanoCoreUsageSecond: 450,
		},
	} {
		t.Run(test.desc, func(t *testing.T) {
			container, err := containerstore.NewContainer(
				containerstore.Metadata{ID: ID},
			)
			assert.NoError(t, err)

			// calculate for first iteration
			// first run so container stats will be nil
			assert.Nil(t, container.Stats)
			cpuUsage := getUsageNanoCores(test.firstCPUValue, container.Stats, timestamp.UnixNano())
			assert.NoError(t, err)
			assert.Equal(t, test.expectedNanoCoreUsageFirst, cpuUsage)

			// fill in the stats as if they now exist
			container.Stats = &stats.ContainerStats{}
			container.Stats.UsageCoreNanoSeconds = test.firstCPUValue
			container.Stats.Timestamp = timestamp
			assert.NotNil(t, container.Stats)

			// calculate for second iteration
			cpuUsage = getUsageNanoCores(test.secondCPUValue, container.Stats, secondAfterTimeStamp.UnixNano())
			assert.NoError(t, err)
			assert.Equal(t, test.expectedNanoCoreUsageSecond, cpuUsage)
		})
	}

}

func Test_criService_podSandboxStats(t *testing.T) {
	initialStatsTimestamp := time.Now()
	currentStatsTimestamp := initialStatsTimestamp.Add(time.Second)

	c := newTestCRIService()

	type expectedStats struct {
		UsageCoreNanoSeconds uint64
		UsageNanoCores       uint64
		WorkingSetBytes      *runtime.UInt64Value
		CommitMemoryBytes    *runtime.UInt64Value
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
					Container: windowsStat(currentStatsTimestamp, 200, memoryStat(20, 250)),
				},
				"s1": {
					Container: windowsStat(currentStatsTimestamp, 200, memoryStat(30, 50)),
				},
			},
			sandbox: sandboxstore.Sandbox{Metadata: sandboxstore.Metadata{ID: "s1"}},
			containers: []containerstore.Container{
				newContainer("c1", running, nil),
			},
			expectedPodStats: &expectedStats{
				UsageCoreNanoSeconds: 400,
				UsageNanoCores:       0,
				WorkingSetBytes:      &runtime.UInt64Value{Value: 50},
				CommitMemoryBytes:    &runtime.UInt64Value{Value: 300},
			},
			expectedContainerStats: []expectedStats{
				{
					UsageCoreNanoSeconds: 200,
					UsageNanoCores:       0,
					WorkingSetBytes:      &runtime.UInt64Value{Value: 20},
					CommitMemoryBytes:    &runtime.UInt64Value{Value: 250},
				},
			},
			expectError: false,
		},
		{
			desc: "pod stats will include the init container stats",
			metrics: map[string]*wstats.Statistics{
				"c1": {
					Container: windowsStat(currentStatsTimestamp, 200, memoryStat(25, 200)),
				},
				"s1": {
					Container: windowsStat(currentStatsTimestamp, 200, memoryStat(10, 50)),
				},
				"i1": {
					Container: windowsStat(currentStatsTimestamp, 200, memoryStat(33, 234)),
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
				WorkingSetBytes:      &runtime.UInt64Value{Value: 68},
				CommitMemoryBytes:    &runtime.UInt64Value{Value: 484},
			},
			expectedContainerStats: []expectedStats{
				{
					UsageCoreNanoSeconds: 200,
					UsageNanoCores:       0,
					WorkingSetBytes:      &runtime.UInt64Value{Value: 25},
					CommitMemoryBytes:    &runtime.UInt64Value{Value: 200},
				},
				{
					UsageCoreNanoSeconds: 200,
					UsageNanoCores:       0,
					WorkingSetBytes:      &runtime.UInt64Value{Value: 33},
					CommitMemoryBytes:    &runtime.UInt64Value{Value: 234},
				},
			},
			expectError: false,
		},
		{
			desc: "pod stats will not include the init container stats if it is stopped",
			metrics: map[string]*wstats.Statistics{
				"c1": {
					Container: windowsStat(currentStatsTimestamp, 200, memoryStat(25, 100)),
				},
				"s1": {
					Container: windowsStat(currentStatsTimestamp, 200, memoryStat(30, 200)),
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
				WorkingSetBytes:      &runtime.UInt64Value{Value: 55},
				CommitMemoryBytes:    &runtime.UInt64Value{Value: 300},
			},
			expectedContainerStats: []expectedStats{
				{
					UsageCoreNanoSeconds: 200,
					UsageNanoCores:       0,
					WorkingSetBytes:      &runtime.UInt64Value{Value: 25},
					CommitMemoryBytes:    &runtime.UInt64Value{Value: 100},
				},
			},
			expectError: false,
		},
		{
			desc: "pod stats will not include the init container stats if it is stopped in failed state",
			metrics: map[string]*wstats.Statistics{
				"c1": {
					Container: windowsStat(currentStatsTimestamp, 200, memoryStat(25, 100)),
				},
				"s1": {
					Container: windowsStat(currentStatsTimestamp, 200, memoryStat(30, 200)),
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
				WorkingSetBytes:      &runtime.UInt64Value{Value: 55},
				CommitMemoryBytes:    &runtime.UInt64Value{Value: 300},
			},
			expectedContainerStats: []expectedStats{
				{
					UsageCoreNanoSeconds: 200,
					UsageNanoCores:       0,
					WorkingSetBytes:      &runtime.UInt64Value{Value: 25},
					CommitMemoryBytes:    &runtime.UInt64Value{Value: 100},
				},
			},
			expectError: false,
		},
		{
			desc: "pod with existing stats will have usagenanocores totalled across pods and containers",
			metrics: map[string]*wstats.Statistics{
				"c1": {
					Container: windowsStat(currentStatsTimestamp, 400, memoryStat(25, 100)),
				},
				"s1": {
					Container: windowsStat(currentStatsTimestamp, 400, memoryStat(20, 20)),
				},
			},
			sandbox: sandboxPod("s1", initialStatsTimestamp, 400),
			containers: []containerstore.Container{
				newContainer("c1", running, &stats.ContainerStats{
					Timestamp:            initialStatsTimestamp,
					UsageCoreNanoSeconds: 200,
				}),
			},
			expectedPodStats: &expectedStats{
				UsageCoreNanoSeconds: 800,
				UsageNanoCores:       400,
				WorkingSetBytes:      &runtime.UInt64Value{Value: 45},
				CommitMemoryBytes:    &runtime.UInt64Value{Value: 120},
			},
			expectedContainerStats: []expectedStats{
				{
					UsageCoreNanoSeconds: 400,
					UsageNanoCores:       200,
					WorkingSetBytes:      &runtime.UInt64Value{Value: 25},
					CommitMemoryBytes:    &runtime.UInt64Value{Value: 100},
				},
			},
			expectError: false,
		},
		{
			desc: "pod sandbox with nil stats still works (hostprocess container scenario)",
			metrics: map[string]*wstats.Statistics{
				"c1": {
					Container: windowsStat(currentStatsTimestamp, 400, memoryStat(20, 40)),
				},
				"s1": nil,
			},
			sandbox: sandboxPod("s1", initialStatsTimestamp, 200),
			containers: []containerstore.Container{
				newContainer("c1", running, &stats.ContainerStats{
					Timestamp:            initialStatsTimestamp,
					UsageCoreNanoSeconds: 200,
				}),
			},
			expectedPodStats: &expectedStats{
				UsageCoreNanoSeconds: 400,
				UsageNanoCores:       200,
				WorkingSetBytes:      &runtime.UInt64Value{Value: 20},
				CommitMemoryBytes:    &runtime.UInt64Value{Value: 40},
			},
			expectedContainerStats: []expectedStats{
				{
					UsageCoreNanoSeconds: 400,
					UsageNanoCores:       200,
					WorkingSetBytes:      &runtime.UInt64Value{Value: 20},
					CommitMemoryBytes:    &runtime.UInt64Value{Value: 40},
				},
			},
			expectError: false,
		},
		{
			desc: "pod sandbox with empty stats still works (hostprocess container scenario)",
			metrics: map[string]*wstats.Statistics{
				"c1": {
					Container: windowsStat(currentStatsTimestamp, 400, memoryStat(20, 41)),
				},
				"s1": {},
			},
			sandbox: sandboxPod("s1", initialStatsTimestamp, 200),
			containers: []containerstore.Container{
				newContainer("c1", running, &stats.ContainerStats{
					Timestamp:            initialStatsTimestamp,
					UsageCoreNanoSeconds: 200,
				}),
			},
			expectedPodStats: &expectedStats{
				UsageCoreNanoSeconds: 400,
				UsageNanoCores:       200,
				WorkingSetBytes:      &runtime.UInt64Value{Value: 20},
				CommitMemoryBytes:    &runtime.UInt64Value{Value: 41},
			},
			expectedContainerStats: []expectedStats{
				{
					UsageCoreNanoSeconds: 400,
					UsageNanoCores:       200,
					WorkingSetBytes:      &runtime.UInt64Value{Value: 20},
					CommitMemoryBytes:    &runtime.UInt64Value{Value: 41},
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
			sandbox: sandboxPod("s1", initialStatsTimestamp, 200),
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
			sandbox:                sandboxPod("s1", initialStatsTimestamp, 200),
			containers:             []containerstore.Container{},
			expectedPodStats:       nil,
			expectedContainerStats: []expectedStats{},
			expectError:            true,
		}, {
			desc: "container with no memory stats should be skipped gracefully",
			metrics: map[string]*wstats.Statistics{
				"c1": {
					Container: windowsStat(currentStatsTimestamp, 100, nil),
				},
				"s1": {
					Container: windowsStat(currentStatsTimestamp, 100, memoryStat(30, 50)),
				},
			},
			sandbox: sandboxstore.Sandbox{Metadata: sandboxstore.Metadata{ID: "s1"}},
			containers: []containerstore.Container{
				newContainer("c1", running, nil),
			},
			expectedPodStats: &expectedStats{
				UsageCoreNanoSeconds: 200,
				UsageNanoCores:       0,
				WorkingSetBytes:      &runtime.UInt64Value{Value: 30},
				CommitMemoryBytes:    &runtime.UInt64Value{Value: 50},
			},
			expectedContainerStats: []expectedStats{
				{
					UsageCoreNanoSeconds: 100,
					UsageNanoCores:       0,
					WorkingSetBytes:      nil, // missing
					CommitMemoryBytes:    nil, // missing
				},
			},
			expectError: false,
		},
	} {
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

			assert.Equal(t, test.expectedPodStats.UsageCoreNanoSeconds, actualPodStats.Cpu.UsageCoreNanoSeconds.Value, "UsageCoreNanoSeconds should match for pod")
			assert.Equalf(t, test.expectedPodStats.UsageNanoCores, actualPodStats.Cpu.UsageNanoCores.Value, "%s: UsageNanoCores should match for pod, (expected: %d, actual: %d)", test.desc, test.expectedPodStats.UsageNanoCores, actualPodStats.Cpu.UsageNanoCores.Value)
			assert.Equalf(t, test.expectedPodStats.CommitMemoryBytes, actualPodStats.Memory.CommitMemoryBytes, "%s: CommitMemoryBytes should match for pod, (expected: %d, actual: %d)", test.desc, test.expectedPodStats.CommitMemoryBytes, actualPodStats.Memory.CommitMemoryBytes.Value)
			assert.Equalf(t, test.expectedPodStats.WorkingSetBytes, actualPodStats.Memory.WorkingSetBytes, "%s: WorkingSetBytes should match for pod, (expected: %d, actual: %d)", test.desc, test.expectedPodStats.WorkingSetBytes, actualPodStats.Memory.WorkingSetBytes.Value)

			for i, expectedStat := range test.expectedContainerStats {
				actualStat := actualContainerStats[i]

				assert.Equalf(t, expectedStat.UsageCoreNanoSeconds, actualStat.Cpu.UsageCoreNanoSeconds.Value, "%s: UsageCoreNanoSeconds should match for container %d, (expected: %d, actual: %d)", test.desc, i, expectedStat.UsageCoreNanoSeconds, actualStat.Cpu.UsageCoreNanoSeconds.Value)
				assert.Equalf(t, expectedStat.UsageNanoCores, actualStat.Cpu.UsageNanoCores.Value, "%s: UsageNanoCores should match for container %d, (expected: %d, actual: %d)", test.desc, i, expectedStat.UsageNanoCores, actualStat.Cpu.UsageNanoCores.Value)
				if expectedStat.WorkingSetBytes == nil || expectedStat.CommitMemoryBytes == nil {
					assert.Nilf(t, actualStat.Memory, "%s: Memory stats should be nil for container %d", test.desc, i)
				} else {
					assert.Equalf(t, expectedStat.WorkingSetBytes, actualStat.Memory.WorkingSetBytes, "%s: WorkingSetBytes should match for container %d, (expected: %d, actual: %d)", test.desc, i, expectedStat.WorkingSetBytes, actualStat.Memory.WorkingSetBytes.Value)
					assert.Equalf(t, expectedStat.CommitMemoryBytes, actualStat.Memory.CommitMemoryBytes, "%s: CommitMemoryBytes should match for container %d, (expected: %d, actual: %d)", test.desc, i, expectedStat.CommitMemoryBytes, actualStat.Memory.CommitMemoryBytes.Value)
				}
			}
		})
	}
}

func sandboxPod(id string, timestamp time.Time, cachedCPU uint64) sandboxstore.Sandbox {
	return sandboxstore.Sandbox{
		Metadata: sandboxstore.Metadata{ID: id, RuntimeHandler: "runc"},
		Stats: &stats.ContainerStats{
			Timestamp:            timestamp,
			UsageCoreNanoSeconds: cachedCPU,
		}}
}

func windowsStat(timestamp time.Time, cpu uint64, memory *wstats.WindowsContainerMemoryStatistics) *wstats.Statistics_Windows {
	return &wstats.Statistics_Windows{
		Windows: &wstats.WindowsContainerStatistics{
			Timestamp: protobuf.ToTimestamp(timestamp),
			Processor: &wstats.WindowsContainerProcessorStatistics{
				TotalRuntimeNS: cpu,
			},
			Memory: memory,
		},
	}
}

func memoryStat(workingSetMemory uint64, commitMemory uint64) *wstats.WindowsContainerMemoryStatistics {
	return &wstats.WindowsContainerMemoryStatistics{
		MemoryUsagePrivateWorkingSetBytes: workingSetMemory,
		MemoryUsageCommitBytes:            commitMemory,
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

func Test_criService_saveSandBoxMetrics(t *testing.T) {

	timestamp := time.Now()
	containerID := "c1"
	sandboxID := "s1"
	for _, test := range []struct {
		desc                   string
		sandboxStats           *runtime.PodSandboxStats
		expectError            bool
		expectedSandboxvalue   *stats.ContainerStats
		expectedContainervalue *stats.ContainerStats
	}{
		{
			desc:                 "if sandboxstats is nil then skip ",
			sandboxStats:         nil,
			expectError:          false,
			expectedSandboxvalue: nil,
		},
		{
			desc: "if sandboxstats.windows is nil then skip",
			sandboxStats: &runtime.PodSandboxStats{
				Windows: nil,
			},
			expectError:          false,
			expectedSandboxvalue: nil,
		},
		{
			desc: "if sandboxstats.windows.cpu is nil then skip",
			sandboxStats: &runtime.PodSandboxStats{
				Windows: &runtime.WindowsPodSandboxStats{
					Cpu: nil,
				},
			},
			expectError:          false,
			expectedSandboxvalue: nil,
		},
		{
			desc: "if sandboxstats.windows.cpu.UsageCoreNanoSeconds is nil then skip",
			sandboxStats: &runtime.PodSandboxStats{
				Windows: &runtime.WindowsPodSandboxStats{
					Cpu: &runtime.WindowsCpuUsage{
						UsageCoreNanoSeconds: nil,
					},
				},
			},
			expectError:          false,
			expectedSandboxvalue: nil,
		},
		{
			desc: "Stats for containers that have cpu nil are skipped",
			sandboxStats: &runtime.PodSandboxStats{
				Windows: &runtime.WindowsPodSandboxStats{
					Cpu: &runtime.WindowsCpuUsage{
						Timestamp:            timestamp.UnixNano(),
						UsageCoreNanoSeconds: &runtime.UInt64Value{Value: 100},
					},
					Containers: []*runtime.WindowsContainerStats{
						{
							Attributes: &runtime.ContainerAttributes{Id: containerID},
							Cpu:        nil,
						},
					},
				},
			},
			expectError: false,
			expectedSandboxvalue: &stats.ContainerStats{
				Timestamp:            timestamp,
				UsageCoreNanoSeconds: 100,
			},
			expectedContainervalue: nil,
		},
		{
			desc: "Stats for containers that have UsageCoreNanoSeconds nil are skipped",
			sandboxStats: &runtime.PodSandboxStats{
				Windows: &runtime.WindowsPodSandboxStats{
					Cpu: &runtime.WindowsCpuUsage{
						Timestamp:            timestamp.UnixNano(),
						UsageCoreNanoSeconds: &runtime.UInt64Value{Value: 100},
					},
					Containers: []*runtime.WindowsContainerStats{
						{
							Attributes: &runtime.ContainerAttributes{Id: containerID},
							Cpu: &runtime.WindowsCpuUsage{
								Timestamp:            timestamp.UnixNano(),
								UsageCoreNanoSeconds: nil},
						},
					},
				},
			},
			expectError: false,
			expectedSandboxvalue: &stats.ContainerStats{
				Timestamp:            timestamp,
				UsageCoreNanoSeconds: 100,
			},
			expectedContainervalue: nil,
		},
		{
			desc: "Stats are updated for sandbox and containers",
			sandboxStats: &runtime.PodSandboxStats{
				Windows: &runtime.WindowsPodSandboxStats{
					Cpu: &runtime.WindowsCpuUsage{
						Timestamp:            timestamp.UnixNano(),
						UsageCoreNanoSeconds: &runtime.UInt64Value{Value: 100},
					},
					Containers: []*runtime.WindowsContainerStats{
						{
							Attributes: &runtime.ContainerAttributes{Id: containerID},
							Cpu: &runtime.WindowsCpuUsage{
								Timestamp:            timestamp.UnixNano(),
								UsageCoreNanoSeconds: &runtime.UInt64Value{Value: 50},
							},
						},
					},
				},
			},
			expectError: false,
			expectedSandboxvalue: &stats.ContainerStats{
				Timestamp:            timestamp,
				UsageCoreNanoSeconds: 100,
			},
			expectedContainervalue: &stats.ContainerStats{
				Timestamp:            timestamp,
				UsageCoreNanoSeconds: 50,
			},
		},
	} {
		t.Run(test.desc, func(t *testing.T) {
			c := newTestCRIService()
			c.sandboxStore.Add(sandboxstore.Sandbox{
				Metadata: sandboxstore.Metadata{ID: sandboxID},
			})

			c.containerStore.Add(containerstore.Container{
				Metadata: containerstore.Metadata{ID: containerID},
			})

			err := c.saveSandBoxMetrics(sandboxID, test.sandboxStats)

			if test.expectError {
				assert.NotNil(t, err)
			} else {
				assert.Nil(t, err)
			}

			sandbox, err := c.sandboxStore.Get(sandboxID)
			assert.Nil(t, err)

			if test.expectedSandboxvalue != nil {
				assert.Equal(t, test.expectedSandboxvalue.Timestamp.UnixNano(), sandbox.Stats.Timestamp.UnixNano())
				assert.Equal(t, test.expectedSandboxvalue.UsageCoreNanoSeconds, sandbox.Stats.UsageCoreNanoSeconds)
			} else {
				assert.Nil(t, sandbox.Stats)
			}

			container, err := c.containerStore.Get(containerID)
			assert.Nil(t, err)
			if test.expectedContainervalue != nil {
				assert.Equal(t, test.expectedContainervalue.Timestamp.UnixNano(), container.Stats.Timestamp.UnixNano())
				assert.Equal(t, test.expectedContainervalue.UsageCoreNanoSeconds, container.Stats.UsageCoreNanoSeconds)
			} else {
				assert.Nil(t, container.Stats)
			}
		})
	}
}
