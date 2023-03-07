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

package sbserver

import (
	"testing"
	"time"

	wstats "github.com/Microsoft/hcsshim/cmd/containerd-shim-runhcs-v1/stats"
	containerstore "github.com/containerd/containerd/pkg/cri/store/container"
	sandboxstore "github.com/containerd/containerd/pkg/cri/store/sandbox"
	"github.com/containerd/containerd/pkg/cri/store/stats"
	"github.com/stretchr/testify/assert"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestGetUsageNanoCores(t *testing.T) {
	timestamp := time.Now()
	secondAfterTimeStamp := timestamp.Add(time.Second)
	ID := "ID"

	for desc, test := range map[string]struct {
		firstCPUValue               uint64
		secondCPUValue              uint64
		expectedNanoCoreUsageFirst  uint64
		expectedNanoCoreUsageSecond uint64
	}{
		"metrics": {
			firstCPUValue:               50,
			secondCPUValue:              500,
			expectedNanoCoreUsageFirst:  0,
			expectedNanoCoreUsageSecond: 450,
		},
	} {
		t.Run(desc, func(t *testing.T) {
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
		WorkingSetBytes      uint64
	}
	for desc, test := range map[string]struct {
		metrics                map[string]*wstats.Statistics
		sandbox                sandboxstore.Sandbox
		containers             []containerstore.Container
		expectedPodStats       expectedStats
		expectedContainerStats []expectedStats
		expectError            bool
	}{
		"no metrics found should return error": {
			metrics:                map[string]*wstats.Statistics{},
			sandbox:                sandboxstore.Sandbox{},
			containers:             []containerstore.Container{},
			expectedPodStats:       expectedStats{},
			expectedContainerStats: []expectedStats{},
			expectError:            true,
		},
		"pod stats will include the container stats": {
			metrics: map[string]*wstats.Statistics{
				"c1": {
					Container: windowsStat(currentStatsTimestamp, 200, 20),
				},
				"s1": {
					Container: windowsStat(currentStatsTimestamp, 200, 20),
				},
			},
			sandbox: sandboxstore.Sandbox{Metadata: sandboxstore.Metadata{ID: "s1"}},
			containers: []containerstore.Container{
				{Metadata: containerstore.Metadata{ID: "c1"}},
			},
			expectedPodStats: expectedStats{
				UsageCoreNanoSeconds: 400,
				UsageNanoCores:       0,
				WorkingSetBytes:      40,
			},
			expectedContainerStats: []expectedStats{
				{
					UsageCoreNanoSeconds: 200,
					UsageNanoCores:       0,
					WorkingSetBytes:      20,
				},
			},
			expectError: false,
		},
		"pod with existing stats will have usagenanocores totalled across pods and containers": {
			metrics: map[string]*wstats.Statistics{
				"c1": {
					Container: windowsStat(currentStatsTimestamp, 400, 20),
				},
				"s1": {
					Container: windowsStat(currentStatsTimestamp, 400, 20),
				},
			},
			sandbox: sandboxPod("s1", initialStatsTimestamp, 400),
			containers: []containerstore.Container{
				{
					Metadata: containerstore.Metadata{ID: "c1"},
					Stats: &stats.ContainerStats{
						Timestamp:            initialStatsTimestamp,
						UsageCoreNanoSeconds: 200,
					},
				},
			},
			expectedPodStats: expectedStats{
				UsageCoreNanoSeconds: 800,
				UsageNanoCores:       400,
				WorkingSetBytes:      40,
			},
			expectedContainerStats: []expectedStats{
				{
					UsageCoreNanoSeconds: 400,
					UsageNanoCores:       200,
					WorkingSetBytes:      20,
				},
			},
			expectError: false,
		},
		"pod sandbox with nil stats still works (hostprocess container scenario)": {
			metrics: map[string]*wstats.Statistics{
				"c1": {
					Container: windowsStat(currentStatsTimestamp, 400, 20),
				},
				"s1": nil,
			},
			sandbox: sandboxPod("s1", initialStatsTimestamp, 200),
			containers: []containerstore.Container{
				{
					Metadata: containerstore.Metadata{ID: "c1"},
					Stats: &stats.ContainerStats{
						Timestamp:            initialStatsTimestamp,
						UsageCoreNanoSeconds: 200,
					},
				},
			},
			expectedPodStats: expectedStats{
				UsageCoreNanoSeconds: 400,
				UsageNanoCores:       200,
				WorkingSetBytes:      20,
			},
			expectedContainerStats: []expectedStats{
				{
					UsageCoreNanoSeconds: 400,
					UsageNanoCores:       200,
					WorkingSetBytes:      20,
				},
			},
			expectError: false,
		},
	} {
		t.Run(desc, func(t *testing.T) {
			actualPodStats, actualContainerStats, err := c.toPodSandboxStats(test.sandbox, test.metrics, test.containers, currentStatsTimestamp)
			if test.expectError {
				assert.NotNil(t, err)
				return
			}

			assert.Equal(t, test.expectedPodStats.UsageCoreNanoSeconds, actualPodStats.Cpu.UsageCoreNanoSeconds.Value)
			assert.Equal(t, test.expectedPodStats.UsageNanoCores, actualPodStats.Cpu.UsageNanoCores.Value)

			for i, expectedStat := range test.expectedContainerStats {
				actutalStat := actualContainerStats[i]

				assert.Equal(t, expectedStat.UsageCoreNanoSeconds, actutalStat.Cpu.UsageCoreNanoSeconds.Value)
				assert.Equal(t, expectedStat.UsageNanoCores, actutalStat.Cpu.UsageNanoCores.Value)
			}
		})
	}
}

func sandboxPod(id string, timestamp time.Time, cachedCPU uint64) sandboxstore.Sandbox {
	return sandboxstore.Sandbox{
		Metadata: sandboxstore.Metadata{ID: id}, Stats: &stats.ContainerStats{
			Timestamp:            timestamp,
			UsageCoreNanoSeconds: cachedCPU,
		}}
}

func windowsStat(timestamp time.Time, cpu uint64, memory uint64) *wstats.Statistics_Windows {
	return &wstats.Statistics_Windows{
		Windows: &wstats.WindowsContainerStatistics{
			Timestamp: timestamp,
			Processor: &wstats.WindowsContainerProcessorStatistics{
				TotalRuntimeNS: cpu,
			},
			Memory: &wstats.WindowsContainerMemoryStatistics{
				MemoryUsagePrivateWorkingSetBytes: memory,
			},
		},
	}
}

func Test_criService_saveSandBoxMetrics(t *testing.T) {

	timestamp := time.Now()
	containerID := "c1"
	sandboxID := "s1"
	for desc, test := range map[string]struct {
		sandboxStats           *runtime.PodSandboxStats
		expectError            bool
		expectedSandboxvalue   *stats.ContainerStats
		expectedContainervalue *stats.ContainerStats
	}{
		"if sandboxstats is nil then skip ": {
			sandboxStats:         nil,
			expectError:          false,
			expectedSandboxvalue: nil,
		},
		"if sandboxstats.windows is nil then skip": {
			sandboxStats: &runtime.PodSandboxStats{
				Windows: nil,
			},
			expectError:          false,
			expectedSandboxvalue: nil,
		},
		"if sandboxstats.windows.cpu is nil then skip": {
			sandboxStats: &runtime.PodSandboxStats{
				Windows: &runtime.WindowsPodSandboxStats{
					Cpu: nil,
				},
			},
			expectError:          false,
			expectedSandboxvalue: nil,
		},
		"if sandboxstats.windows.cpu.UsageCoreNanoSeconds is nil then skip": {
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
		"Stats for containers that have cpu nil are skipped": {
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
		"Stats for containers that have UsageCoreNanoSeconds nil are skipped": {
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
		"Stats are updated for sandbox and containers": {
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
		t.Run(desc, func(t *testing.T) {
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
