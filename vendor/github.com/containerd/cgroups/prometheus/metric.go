package prometheus

import (
	"github.com/containerd/cgroups"
	metrics "github.com/docker/go-metrics"
	"github.com/prometheus/client_golang/prometheus"
)

type value struct {
	v float64
	l []string
}

type metric struct {
	name   string
	help   string
	unit   metrics.Unit
	vt     prometheus.ValueType
	labels []string
	// getValues returns the value and labels for the data
	getValues func(stats *cgroups.Stats) []value
}

func (m *metric) desc(ns *metrics.Namespace) *prometheus.Desc {
	return ns.NewDesc(m.name, m.help, m.unit, append([]string{"id"}, m.labels...)...)
}

func (m *metric) collect(id string, stats *cgroups.Stats, ns *metrics.Namespace, ch chan<- prometheus.Metric) {
	values := m.getValues(stats)
	for _, v := range values {
		ch <- prometheus.MustNewConstMetric(m.desc(ns), m.vt, v.v, append([]string{id}, v.l...)...)
	}
}
