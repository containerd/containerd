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
	"math"
	"testing"
	"time"

	v1 "github.com/containerd/cgroups/stats/v1"
	v2 "github.com/containerd/cgroups/v2/stats"
	containerstore "github.com/containerd/containerd/pkg/cri/store/container"
	"github.com/stretchr/testify/assert"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestGetWorkingSet(t *testing.T) {
	for desc, test := range map[string]struct {
		memory   *v1.MemoryStat
		expected uint64
	}{
		"nil memory usage": {
			memory:   &v1.MemoryStat{},
			expected: 0,
		},
		"memory usage higher than inactive_total_file": {
			memory: &v1.MemoryStat{
				TotalInactiveFile: 1000,
				Usage:             &v1.MemoryEntry{Usage: 2000},
			},
			expected: 1000,
		},
		"memory usage lower than inactive_total_file": {
			memory: &v1.MemoryStat{
				TotalInactiveFile: 2000,
				Usage:             &v1.MemoryEntry{Usage: 1000},
			},
			expected: 0,
		},
	} {
		t.Run(desc, func(t *testing.T) {
			got := getWorkingSet(test.memory)
			assert.Equal(t, test.expected, got)
		})
	}
}

func TestGetWorkingSetV2(t *testing.T) {
	for desc, test := range map[string]struct {
		memory   *v2.MemoryStat
		expected uint64
	}{
		"nil memory usage": {
			memory:   &v2.MemoryStat{},
			expected: 0,
		},
		"memory usage higher than inactive_total_file": {
			memory: &v2.MemoryStat{
				InactiveFile: 1000,
				Usage:        2000,
			},
			expected: 1000,
		},
		"memory usage lower than inactive_total_file": {
			memory: &v2.MemoryStat{
				InactiveFile: 2000,
				Usage:        1000,
			},
			expected: 0,
		},
	} {
		t.Run(desc, func(t *testing.T) {
			got := getWorkingSetV2(test.memory)
			assert.Equal(t, test.expected, got)
		})
	}
}

func TestGetAvailableBytes(t *testing.T) {
	for desc, test := range map[string]struct {
		memory          *v1.MemoryStat
		workingSetBytes uint64
		expected        uint64
	}{

		"no limit": {
			memory: &v1.MemoryStat{
				Usage: &v1.MemoryEntry{
					Limit: math.MaxUint64, // no limit
					Usage: 1000,
				},
			},
			workingSetBytes: 500,
			expected:        0,
		},
		"with limit": {
			memory: &v1.MemoryStat{
				Usage: &v1.MemoryEntry{
					Limit: 5000,
					Usage: 1000,
				},
			},
			workingSetBytes: 500,
			expected:        5000 - 500,
		},
	} {
		t.Run(desc, func(t *testing.T) {
			got := getAvailableBytes(test.memory, test.workingSetBytes)
			assert.Equal(t, test.expected, got)
		})
	}
}

func TestGetAvailableBytesV2(t *testing.T) {
	for desc, test := range map[string]struct {
		memory          *v2.MemoryStat
		workingSetBytes uint64
		expected        uint64
	}{

		"no limit": {
			memory: &v2.MemoryStat{
				UsageLimit: math.MaxUint64, // no limit
				Usage:      1000,
			},
			workingSetBytes: 500,
			expected:        0,
		},
		"with limit": {
			memory: &v2.MemoryStat{
				UsageLimit: 5000,
				Usage:      1000,
			},
			workingSetBytes: 500,
			expected:        5000 - 500,
		},
	} {
		t.Run(desc, func(t *testing.T) {
			got := getAvailableBytesV2(test.memory, test.workingSetBytes)
			assert.Equal(t, test.expected, got)
		})
	}
}

func TestContainerMetricsCPU(t *testing.T) {
	c := newTestCRIService()
	timestamp := time.Now()
	secondAfterTimeStamp := timestamp.Add(time.Second)
	ID := "ID"

	for desc, test := range map[string]struct {
		firstMetrics   interface{}
		secondMetrics  interface{}
		expectedFirst  *runtime.CpuUsage
		expectedSecond *runtime.CpuUsage
	}{

		"v1 metrics": {
			firstMetrics: &v1.Metrics{
				CPU: &v1.CPUStat{
					Usage: &v1.CPUUsage{
						Total: 50,
					},
				},
			},
			secondMetrics: &v1.Metrics{
				CPU: &v1.CPUStat{
					Usage: &v1.CPUUsage{
						Total: 500,
					},
				},
			},
			expectedFirst: &runtime.CpuUsage{
				Timestamp:            timestamp.UnixNano(),
				UsageCoreNanoSeconds: &runtime.UInt64Value{Value: 50},
				UsageNanoCores:       &runtime.UInt64Value{Value: 0},
			},
			expectedSecond: &runtime.CpuUsage{
				Timestamp:            secondAfterTimeStamp.UnixNano(),
				UsageCoreNanoSeconds: &runtime.UInt64Value{Value: 500},
				UsageNanoCores:       &runtime.UInt64Value{Value: 450},
			},
		},
	} {
		t.Run(desc, func(t *testing.T) {
			container, err := containerstore.NewContainer(
				containerstore.Metadata{ID: ID},
			)
			assert.NoError(t, err)
			assert.Nil(t, container.Stats)
			err = c.containerStore.Add(container)
			assert.NoError(t, err)

			cpuUsage, err := c.cpuContainerStats(ID, false, test.firstMetrics, timestamp)
			assert.NoError(t, err)

			container, err = c.containerStore.Get(ID)
			assert.NoError(t, err)
			assert.NotNil(t, container.Stats)

			assert.Equal(t, test.expectedFirst, cpuUsage)

			cpuUsage, err = c.cpuContainerStats(ID, false, test.secondMetrics, secondAfterTimeStamp)
			assert.NoError(t, err)
			assert.Equal(t, test.expectedSecond, cpuUsage)

			container, err = c.containerStore.Get(ID)
			assert.NoError(t, err)
			assert.NotNil(t, container.Stats)
		})
	}

}

func TestContainerMetricsMemory(t *testing.T) {
	c := newTestCRIService()
	timestamp := time.Now()

	for desc, test := range map[string]struct {
		metrics  interface{}
		expected *runtime.MemoryUsage
	}{
		"v1 metrics - no memory limit": {
			metrics: &v1.Metrics{
				Memory: &v1.MemoryStat{
					Usage: &v1.MemoryEntry{
						Limit: math.MaxUint64, // no limit
						Usage: 1000,
					},
					TotalRSS:          10,
					TotalPgFault:      11,
					TotalPgMajFault:   12,
					TotalInactiveFile: 500,
				},
			},
			expected: &runtime.MemoryUsage{
				Timestamp:       timestamp.UnixNano(),
				WorkingSetBytes: &runtime.UInt64Value{Value: 500},
				AvailableBytes:  &runtime.UInt64Value{Value: 0},
				UsageBytes:      &runtime.UInt64Value{Value: 1000},
				RssBytes:        &runtime.UInt64Value{Value: 10},
				PageFaults:      &runtime.UInt64Value{Value: 11},
				MajorPageFaults: &runtime.UInt64Value{Value: 12},
			},
		},
		"v1 metrics - memory limit": {
			metrics: &v1.Metrics{
				Memory: &v1.MemoryStat{
					Usage: &v1.MemoryEntry{
						Limit: 5000,
						Usage: 1000,
					},
					TotalRSS:          10,
					TotalPgFault:      11,
					TotalPgMajFault:   12,
					TotalInactiveFile: 500,
				},
			},
			expected: &runtime.MemoryUsage{
				Timestamp:       timestamp.UnixNano(),
				WorkingSetBytes: &runtime.UInt64Value{Value: 500},
				AvailableBytes:  &runtime.UInt64Value{Value: 4500},
				UsageBytes:      &runtime.UInt64Value{Value: 1000},
				RssBytes:        &runtime.UInt64Value{Value: 10},
				PageFaults:      &runtime.UInt64Value{Value: 11},
				MajorPageFaults: &runtime.UInt64Value{Value: 12},
			},
		},
		"v2 metrics - memory limit": {
			metrics: &v2.Metrics{
				Memory: &v2.MemoryStat{
					Usage:        1000,
					UsageLimit:   5000,
					InactiveFile: 0,
					Pgfault:      11,
					Pgmajfault:   12,
				},
			},
			expected: &runtime.MemoryUsage{
				Timestamp:       timestamp.UnixNano(),
				WorkingSetBytes: &runtime.UInt64Value{Value: 1000},
				AvailableBytes:  &runtime.UInt64Value{Value: 4000},
				UsageBytes:      &runtime.UInt64Value{Value: 1000},
				RssBytes:        &runtime.UInt64Value{Value: 0},
				PageFaults:      &runtime.UInt64Value{Value: 11},
				MajorPageFaults: &runtime.UInt64Value{Value: 12},
			},
		},
		"v2 metrics - no memory limit": {
			metrics: &v2.Metrics{
				Memory: &v2.MemoryStat{
					Usage:        1000,
					UsageLimit:   math.MaxUint64, // no limit
					InactiveFile: 0,
					Pgfault:      11,
					Pgmajfault:   12,
				},
			},
			expected: &runtime.MemoryUsage{
				Timestamp:       timestamp.UnixNano(),
				WorkingSetBytes: &runtime.UInt64Value{Value: 1000},
				AvailableBytes:  &runtime.UInt64Value{Value: 0},
				UsageBytes:      &runtime.UInt64Value{Value: 1000},
				RssBytes:        &runtime.UInt64Value{Value: 0},
				PageFaults:      &runtime.UInt64Value{Value: 11},
				MajorPageFaults: &runtime.UInt64Value{Value: 12},
			},
		},
	} {
		t.Run(desc, func(t *testing.T) {
			got, err := c.memoryContainerStats("ID", test.metrics, timestamp)
			assert.NoError(t, err)
			assert.Equal(t, test.expected, got)
		})
	}
}
