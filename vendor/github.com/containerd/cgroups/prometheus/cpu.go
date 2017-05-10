package prometheus

import (
	"strconv"

	"github.com/containerd/cgroups"
	metrics "github.com/docker/go-metrics"
	"github.com/prometheus/client_golang/prometheus"
)

var cpuMetrics = []*metric{
	{
		name: "cpu_total",
		help: "The total cpu time",
		unit: metrics.Nanoseconds,
		vt:   prometheus.GaugeValue,
		getValues: func(stats *cgroups.Stats) []value {
			if stats.Cpu == nil {
				return nil
			}
			return []value{
				{
					v: float64(stats.Cpu.Usage.Total),
				},
			}
		},
	},
	{
		name: "cpu_kernel",
		help: "The total kernel cpu time",
		unit: metrics.Nanoseconds,
		vt:   prometheus.GaugeValue,
		getValues: func(stats *cgroups.Stats) []value {
			if stats.Cpu == nil {
				return nil
			}
			return []value{
				{
					v: float64(stats.Cpu.Usage.Kernel),
				},
			}
		},
	},
	{
		name: "cpu_user",
		help: "The total user cpu time",
		unit: metrics.Nanoseconds,
		vt:   prometheus.GaugeValue,
		getValues: func(stats *cgroups.Stats) []value {
			if stats.Cpu == nil {
				return nil
			}
			return []value{
				{
					v: float64(stats.Cpu.Usage.User),
				},
			}
		},
	},
	{
		name:   "per_cpu",
		help:   "The total cpu time per cpu",
		unit:   metrics.Nanoseconds,
		vt:     prometheus.GaugeValue,
		labels: []string{"cpu"},
		getValues: func(stats *cgroups.Stats) []value {
			if stats.Cpu == nil {
				return nil
			}
			var out []value
			for i, v := range stats.Cpu.Usage.PerCpu {
				out = append(out, value{
					v: float64(v),
					l: []string{strconv.Itoa(i)},
				})
			}
			return out
		},
	},
	{
		name: "cpu_throttle_periods",
		help: "The total cpu throttle periods",
		unit: metrics.Total,
		vt:   prometheus.GaugeValue,
		getValues: func(stats *cgroups.Stats) []value {
			if stats.Cpu == nil {
				return nil
			}
			return []value{
				{
					v: float64(stats.Cpu.Throttling.Periods),
				},
			}
		},
	},
	{
		name: "cpu_throttled_periods",
		help: "The total cpu throttled periods",
		unit: metrics.Total,
		vt:   prometheus.GaugeValue,
		getValues: func(stats *cgroups.Stats) []value {
			if stats.Cpu == nil {
				return nil
			}
			return []value{
				{
					v: float64(stats.Cpu.Throttling.ThrottledPeriods),
				},
			}
		},
	},
	{
		name: "cpu_throttled_time",
		help: "The total cpu throttled time",
		unit: metrics.Nanoseconds,
		vt:   prometheus.GaugeValue,
		getValues: func(stats *cgroups.Stats) []value {
			if stats.Cpu == nil {
				return nil
			}
			return []value{
				{
					v: float64(stats.Cpu.Throttling.ThrottledTime),
				},
			}
		},
	},
}
