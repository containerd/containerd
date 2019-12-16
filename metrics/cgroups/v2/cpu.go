// +build linux

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

package v2

import (
	v2 "github.com/containerd/containerd/metrics/types/v2"
	metrics "github.com/docker/go-metrics"
	"github.com/prometheus/client_golang/prometheus"
)

var cpuMetrics = []*metric{
	{
		name: "cpu",
		help: "Current cpu usage_usec (cgroup v2)",
		unit: metrics.Unit("usage_usec"),
		vt:   prometheus.GaugeValue,
		getValues: func(stats *v2.Metrics) []value {
			if stats.CPU == nil {
				return nil
			}
			return []value{
				{
					v: float64(stats.CPU.UsageUsec),
				},
			}
		},
	},
	{
		name: "cpu",
		help: "Current cpu user_usec (cgroup v2)",
		unit: metrics.Unit("user_usec"),
		vt:   prometheus.GaugeValue,
		getValues: func(stats *v2.Metrics) []value {
			if stats.CPU == nil {
				return nil
			}
			return []value{
				{
					v: float64(stats.CPU.UserUsec),
				},
			}
		},
	},
	{
		name: "cpu",
		help: "Current cpu system_usec (cgroup v2)",
		unit: metrics.Unit("system_usec"),
		vt:   prometheus.GaugeValue,
		getValues: func(stats *v2.Metrics) []value {
			if stats.CPU == nil {
				return nil
			}
			return []value{
				{
					v: float64(stats.CPU.SystemUsec),
				},
			}
		},
	},
	{
		name: "cpu",
		help: "Current cpu nr_periods (only if controller is enabled)",
		unit: metrics.Unit("nr_periods"),
		vt:   prometheus.GaugeValue,
		getValues: func(stats *v2.Metrics) []value {
			if stats.CPU == nil {
				return nil
			}
			return []value{
				{
					v: float64(stats.CPU.NrPeriods),
				},
			}
		},
	},
	{
		name: "cpu",
		help: "Current cpu nr_throttled (only if controller is enabled)",
		unit: metrics.Unit("nr_throttled"),
		vt:   prometheus.GaugeValue,
		getValues: func(stats *v2.Metrics) []value {
			if stats.CPU == nil {
				return nil
			}
			return []value{
				{
					v: float64(stats.CPU.NrThrottled),
				},
			}
		},
	},
	{
		name: "cpu",
		help: "Current cpu throttled_usec (only if controller is enabled)",
		unit: metrics.Unit("throttled_usec"),
		vt:   prometheus.GaugeValue,
		getValues: func(stats *v2.Metrics) []value {
			if stats.CPU == nil {
				return nil
			}
			return []value{
				{
					v: float64(stats.CPU.ThrottledUsec),
				},
			}
		},
	},
}
