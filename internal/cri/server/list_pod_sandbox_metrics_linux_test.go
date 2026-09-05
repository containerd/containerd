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

	v2 "github.com/containerd/cgroups/v3/cgroup2/stats"
	"github.com/stretchr/testify/assert"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestExtractCPUPressureMetrics(t *testing.T) {
	c := newTestCRIService()
	labels := []string{"pod", "ns", "ctr", "id", "img"}
	var timestamp int64 = 1000

	stats := cgroupMetrics{v2: &v2.Metrics{
		CPU: &v2.CPUStat{
			PSI: &v2.PSIStats{
				Full: &v2.PSIData{Total: 2000000},
				Some: &v2.PSIData{Total: 5000000},
			},
		},
	}}

	metrics, err := c.extractCPUMetrics(stats, labels, timestamp)
	assert.NoError(t, err)

	stalledMetric := findMetricByName(metrics, "container_pressure_cpu_stalled_seconds_total")
	assert.NotNil(t, stalledMetric, "expected container_pressure_cpu_stalled_seconds_total metric")
	assert.Equal(t, uint64(2), stalledMetric.Value.GetValue())
	assert.Equal(t, labels, stalledMetric.LabelValues)

	waitingMetric := findMetricByName(metrics, "container_pressure_cpu_waiting_seconds_total")
	assert.NotNil(t, waitingMetric, "expected container_pressure_cpu_waiting_seconds_total metric")
	assert.Equal(t, uint64(5), waitingMetric.Value.GetValue())
	assert.Equal(t, labels, waitingMetric.LabelValues)
}

func TestExtractMemoryPressureMetrics(t *testing.T) {
	c := newTestCRIService()
	labels := []string{"pod", "ns", "ctr", "id", "img"}
	var timestamp int64 = 1000

	stats := cgroupMetrics{v2: &v2.Metrics{
		Memory: &v2.MemoryStat{
			PSI: &v2.PSIStats{
				Full: &v2.PSIData{Total: 1000000},
				Some: &v2.PSIData{Total: 3000000},
			},
		},
	}}

	metrics, err := c.extractMemoryMetrics(stats, labels, timestamp)
	assert.NoError(t, err)

	stalledMetric := findMetricByName(metrics, "container_pressure_memory_stalled_seconds_total")
	assert.NotNil(t, stalledMetric, "expected container_pressure_memory_stalled_seconds_total metric")
	assert.Equal(t, uint64(1), stalledMetric.Value.GetValue())
	assert.Equal(t, labels, stalledMetric.LabelValues)

	waitingMetric := findMetricByName(metrics, "container_pressure_memory_waiting_seconds_total")
	assert.NotNil(t, waitingMetric, "expected container_pressure_memory_waiting_seconds_total metric")
	assert.Equal(t, uint64(3), waitingMetric.Value.GetValue())
	assert.Equal(t, labels, waitingMetric.LabelValues)
}

func TestExtractIOPressureMetrics(t *testing.T) {
	c := newTestCRIService()
	labels := []string{"pod", "ns", "ctr", "id", "img"}
	var timestamp int64 = 1000

	stats := cgroupMetrics{v2: &v2.Metrics{
		Io: &v2.IOStat{
			PSI: &v2.PSIStats{
				Full: &v2.PSIData{Total: 4000000},
				Some: &v2.PSIData{Total: 7000000},
			},
		},
	}}

	metrics, err := c.extractDiskIOMetrics(stats, labels, timestamp)
	assert.NoError(t, err)

	stalledMetric := findMetricByName(metrics, "container_pressure_io_stalled_seconds_total")
	assert.NotNil(t, stalledMetric, "expected container_pressure_io_stalled_seconds_total metric")
	assert.Equal(t, uint64(4), stalledMetric.Value.GetValue())
	assert.Equal(t, labels, stalledMetric.LabelValues)

	waitingMetric := findMetricByName(metrics, "container_pressure_io_waiting_seconds_total")
	assert.NotNil(t, waitingMetric, "expected container_pressure_io_waiting_seconds_total metric")
	assert.Equal(t, uint64(7), waitingMetric.Value.GetValue())
	assert.Equal(t, labels, waitingMetric.LabelValues)
}

func TestNoPressureMetricsWithNilPSI(t *testing.T) {
	c := newTestCRIService()
	labels := []string{"pod", "ns", "ctr", "id", "img"}
	var timestamp int64 = 1000

	stats := cgroupMetrics{v2: &v2.Metrics{
		CPU:    &v2.CPUStat{},
		Memory: &v2.MemoryStat{},
		Io:     &v2.IOStat{},
	}}

	cpuMetrics, err := c.extractCPUMetrics(stats, labels, timestamp)
	assert.NoError(t, err)
	assert.Nil(t, findMetricByName(cpuMetrics, "container_pressure_cpu_stalled_seconds_total"))
	assert.Nil(t, findMetricByName(cpuMetrics, "container_pressure_cpu_waiting_seconds_total"))

	memMetrics, err := c.extractMemoryMetrics(stats, labels, timestamp)
	assert.NoError(t, err)
	assert.Nil(t, findMetricByName(memMetrics, "container_pressure_memory_stalled_seconds_total"))
	assert.Nil(t, findMetricByName(memMetrics, "container_pressure_memory_waiting_seconds_total"))

	ioMetrics, err := c.extractDiskIOMetrics(stats, labels, timestamp)
	assert.NoError(t, err)
	assert.Nil(t, findMetricByName(ioMetrics, "container_pressure_io_stalled_seconds_total"))
	assert.Nil(t, findMetricByName(ioMetrics, "container_pressure_io_waiting_seconds_total"))
}

func TestPressureMetricDescriptorsRegistered(t *testing.T) {
	c := newTestCRIService()
	descriptors := c.getMetricDescriptors()

	pressureDescs, ok := descriptors[PressureMetrics]
	assert.True(t, ok, "PressureMetrics key should exist in metric descriptors")
	assert.Len(t, pressureDescs, 6)

	expectedNames := []string{
		"container_pressure_cpu_stalled_seconds_total",
		"container_pressure_cpu_waiting_seconds_total",
		"container_pressure_memory_stalled_seconds_total",
		"container_pressure_memory_waiting_seconds_total",
		"container_pressure_io_stalled_seconds_total",
		"container_pressure_io_waiting_seconds_total",
	}

	for _, name := range expectedNames {
		found := false
		for _, desc := range pressureDescs {
			if desc.Name == name {
				found = true
				break
			}
		}
		assert.True(t, found, "expected descriptor %q to be registered", name)
	}
}

func findMetricByName(metrics []*runtime.Metric, name string) *runtime.Metric {
	for _, m := range metrics {
		if m.Name == name {
			return m
		}
	}
	return nil
}
