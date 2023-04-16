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

	v1 "github.com/containerd/cgroups/v3/cgroup1/stats"
	v2 "github.com/containerd/cgroups/v3/cgroup2/stats"
	"github.com/stretchr/testify/assert"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestGetWorkingSet(t *testing.T) {
	for _, test := range []struct {
		desc     string
		memory   *v1.MemoryStat
		expected uint64
	}{
		{
			desc:     "nil memory usage",
			memory:   &v1.MemoryStat{},
			expected: 0,
		},
		{
			desc: "memory usage higher than inactive_total_file",
			memory: &v1.MemoryStat{
				TotalInactiveFile: 1000,
				Usage:             &v1.MemoryEntry{Usage: 2000},
			},
			expected: 1000,
		},
		{
			desc: "memory usage lower than inactive_total_file",
			memory: &v1.MemoryStat{
				TotalInactiveFile: 2000,
				Usage:             &v1.MemoryEntry{Usage: 1000},
			},
			expected: 0,
		},
	} {
		test := test
		t.Run(test.desc, func(t *testing.T) {
			got := getWorkingSet(test.memory)
			assert.Equal(t, test.expected, got)
		})
	}
}

func TestGetWorkingSetV2(t *testing.T) {
	for _, test := range []struct {
		desc     string
		memory   *v2.MemoryStat
		expected uint64
	}{
		{
			desc:     "nil memory usage",
			memory:   &v2.MemoryStat{},
			expected: 0,
		},
		{
			desc: "memory usage higher than inactive_total_file",
			memory: &v2.MemoryStat{
				InactiveFile: 1000,
				Usage:        2000,
			},
			expected: 1000,
		},
		{
			desc: "memory usage lower than inactive_total_file",
			memory: &v2.MemoryStat{
				InactiveFile: 2000,
				Usage:        1000,
			},
			expected: 0,
		},
	} {
		test := test
		t.Run(test.desc, func(t *testing.T) {
			got := getWorkingSetV2(test.memory)
			assert.Equal(t, test.expected, got)
		})
	}
}

func TestGetAvailableBytes(t *testing.T) {
	for _, test := range []struct {
		desc            string
		memory          *v1.MemoryStat
		workingSetBytes uint64
		expected        uint64
	}{

		{
			desc: "no limit",
			memory: &v1.MemoryStat{
				Usage: &v1.MemoryEntry{
					Limit: math.MaxUint64, // no limit
					Usage: 1000,
				},
			},
			workingSetBytes: 500,
			expected:        0,
		},
		{
			desc: "with limit",
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
		test := test
		t.Run(test.desc, func(t *testing.T) {
			got := getAvailableBytes(test.memory, test.workingSetBytes)
			assert.Equal(t, test.expected, got)
		})
	}
}

func TestGetAvailableBytesV2(t *testing.T) {
	for _, test := range []struct {
		desc            string
		memory          *v2.MemoryStat
		workingSetBytes uint64
		expected        uint64
	}{

		{
			desc: "no limit",
			memory: &v2.MemoryStat{
				UsageLimit: math.MaxUint64, // no limit
				Usage:      1000,
			},
			workingSetBytes: 500,
			expected:        0,
		},
		{
			desc: "with limit",
			memory: &v2.MemoryStat{
				UsageLimit: 5000,
				Usage:      1000,
			},
			workingSetBytes: 500,
			expected:        5000 - 500,
		},
	} {
		test := test
		t.Run(test.desc, func(t *testing.T) {
			got := getAvailableBytesV2(test.memory, test.workingSetBytes)
			assert.Equal(t, test.expected, got)
		})
	}
}

func TestContainerMetricsMemory(t *testing.T) {
	c := newTestCRIService()
	timestamp := time.Now()

	for _, test := range []struct {
		desc     string
		metrics  interface{}
		expected *runtime.MemoryUsage
	}{
		{
			desc: "v1 metrics - no memory limit",
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
		{
			desc: "v1 metrics - memory limit",
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
		{
			desc: "v2 metrics - memory limit",
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
		{
			desc: "v2 metrics - no memory limit",
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
		test := test
		t.Run(test.desc, func(t *testing.T) {
			got, err := c.memoryContainerStats("ID", test.metrics, timestamp)
			assert.NoError(t, err)
			assert.Equal(t, test.expected, got)
		})
	}
}
