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

package images

import (
	"github.com/docker/go-metrics"
	prom "github.com/prometheus/client_golang/prometheus"
)

var (
	imagePulls           metrics.LabeledCounter
	inProgressImagePulls metrics.Gauge
	// image size in MB / image pull duration in seconds
	imagePullThroughput prom.Histogram
)

func init() {
	const (
		namespace = "containerd"
		subsystem = "cri_sandboxed"
	)

	// these CRI metrics record latencies for successful operations around a sandbox and container's lifecycle.
	ns := metrics.NewNamespace(namespace, subsystem, nil)

	imagePulls = ns.NewLabeledCounter("image_pulls", "succeeded and failed counters", "status")
	inProgressImagePulls = ns.NewGauge("in_progress_image_pulls", "in progress pulls", metrics.Total)
	imagePullThroughput = prom.NewHistogram(
		prom.HistogramOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "image_pulling_throughput",
			Help:      "image pull throughput",
			Buckets:   prom.DefBuckets,
		},
	)
	ns.Add(imagePullThroughput)
	metrics.Register(ns)
}
