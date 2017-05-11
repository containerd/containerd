package prometheus

import (
	"github.com/containerd/cgroups"
	metrics "github.com/docker/go-metrics"
	"github.com/prometheus/client_golang/prometheus"
)

var hugetlbMetrics = []*metric{
	{
		name:   "hugetlb_usage",
		help:   "The hugetlb usage",
		unit:   metrics.Bytes,
		vt:     prometheus.GaugeValue,
		labels: []string{"page"},
		getValues: func(stats *cgroups.Stats) []value {
			if stats.Hugetlb == nil {
				return nil
			}
			var out []value
			for page, v := range stats.Hugetlb {
				out = append(out, value{
					v: float64(v.Usage),
					l: []string{page},
				})
			}
			return out
		},
	},
	{
		name:   "hugetlb_failcnt",
		help:   "The hugetlb failcnt",
		unit:   metrics.Total,
		vt:     prometheus.GaugeValue,
		labels: []string{"page"},
		getValues: func(stats *cgroups.Stats) []value {
			if stats.Hugetlb == nil {
				return nil
			}
			var out []value
			for page, v := range stats.Hugetlb {
				out = append(out, value{
					v: float64(v.Failcnt),
					l: []string{page},
				})
			}
			return out
		},
	},
	{
		name:   "hugetlb_max",
		help:   "The hugetlb maximum usage",
		unit:   metrics.Bytes,
		vt:     prometheus.GaugeValue,
		labels: []string{"page"},
		getValues: func(stats *cgroups.Stats) []value {
			if stats.Hugetlb == nil {
				return nil
			}
			var out []value
			for page, v := range stats.Hugetlb {
				out = append(out, value{
					v: float64(v.Max),
					l: []string{page},
				})
			}
			return out
		},
	},
}
