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

var memoryMetrics = []*metric{
	{
		name: "memory",
		help: "Current memory usage (cgroup v2)",
		unit: metrics.Unit("usage"),
		vt:   prometheus.GaugeValue,
		getValues: func(stats *v2.Metrics) []value {
			if stats.Memory == nil {
				return nil
			}
			return []value{
				{
					v: float64(stats.Memory.Usage),
				},
			}
		},
	},
	{
		name: "memory",
		help: "Current memory usage limit (cgroup v2)",
		unit: metrics.Unit("limit"),
		vt:   prometheus.GaugeValue,
		getValues: func(stats *v2.Metrics) []value {
			if stats.Memory == nil {
				return nil
			}
			return []value{
				{
					v: float64(stats.Memory.UsageLimit),
				},
			}
		},
	},
	{
		name: "memory",
		help: "Current swap usage (cgroup v2)",
		unit: metrics.Unit("swap"),
		vt:   prometheus.GaugeValue,
		getValues: func(stats *v2.Metrics) []value {
			if stats.Memory == nil {
				return nil
			}
			return []value{
				{
					v: float64(stats.Memory.SwapUsage),
				},
			}
		},
	},
	{
		name: "memory",
		help: "Current swap usage limit (cgroup v2)",
		unit: metrics.Unit("swap_limit"),
		vt:   prometheus.GaugeValue,
		getValues: func(stats *v2.Metrics) []value {
			if stats.Memory == nil {
				return nil
			}
			return []value{
				{
					v: float64(stats.Memory.SwapLimit),
				},
			}
		},
	},
}
