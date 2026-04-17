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
	"time"

	"github.com/docker/go-metrics"
	prom "github.com/prometheus/client_golang/prometheus"
)

const mbToByte = 1024 * 1024

var (
	imagePulls           metrics.LabeledCounter
	inProgressImagePulls metrics.Gauge
	// imagePullThroughput observes one MB/s sample per image pull (not per
	// layer): fetched bytes over end-to-end pull duration (includes
	// extraction). Fully-cached pulls are skipped.
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
			Help:      "image pull throughput in MB/s (fetched bytes / pull duration; cached layers excluded)",
			Buckets:   prom.DefBuckets,
		},
	)
	ns.Add(imagePullThroughput)
	metrics.Register(ns)
}

// recordImagePullThroughput observes a single pull throughput sample in MB/s.
// bytesPulled is the bytes actually fetched from the registry — cached layers
// never trigger a fetch, so they're naturally excluded. duration is the
// end-to-end pull duration (includes extraction, not just network transfer).
//
// Pulls that fetched nothing (fully cached) are skipped rather than recorded
// as zero or near-infinite samples, which would pollute the histogram.
func recordImagePullThroughput(obs prom.Observer, bytesPulled uint64, duration time.Duration) {
	if bytesPulled == 0 || duration <= 0 {
		return
	}
	obs.Observe(float64(bytesPulled) / mbToByte / duration.Seconds())
}
