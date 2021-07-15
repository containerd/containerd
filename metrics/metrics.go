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

package metrics

import (
	goMetrics "github.com/docker/go-metrics"
	"github.com/prometheus/client_golang/prometheus"
)

// Collector provides the ability to collect generic metrics and export
// them in the prometheus format
type Collector struct {
	ns *goMetrics.Namespace
	m  metric
}

type metric struct {
	name   string
	help   string
	unit   goMetrics.Unit
	vt     prometheus.ValueType
	labels []string
	// getValues returns the value and labels for the data
	getValues func() []value
}

type value struct {
	v float64
	l []string
}

// Describe prometheus metrics
func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	m := c.m
	ch <- c.ns.NewDesc(m.name, m.help, m.unit, m.labels...)
}

// Collect prometheus metrics
func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	m := c.m
	for _, v := range m.getValues() {
		ch <- prometheus.MustNewConstMetric(
			c.ns.NewDesc(m.name, m.help, m.unit, m.labels...),
			m.vt,
			v.v,
			v.l...,
		)
	}
}

// Register a prometheus namespace for generic metrics
func Register() {
	ns := goMetrics.NewNamespace("containerd", "", nil)

	c := newBuildInfoCollector(ns)
	ns.Add(c)
	goMetrics.Register(ns)
}
