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
	"sync/atomic"
	"time"

	profilestore "github.com/containerd/containerd/v2/internal/cri/store/securityprofile"

	"github.com/docker/go-metrics"
	prom "github.com/prometheus/client_golang/prometheus"
)

const mibToByte = 1024 * 1024

var (
	securityProfileMetricsStore atomic.Pointer[profilestore.Store]
	imagePulls                  metrics.LabeledCounter
	inProgressImagePulls        metrics.Gauge
	// imagePullThroughput observes one MiB/s sample per image pull: bytes
	// actually fetched from the registry over end-to-end pull duration.
	// Fully-cached pulls are skipped.
	//
	// Deprecated: use imagePullThroughputMiBps instead. The new metric has
	// the same semantics but more useful buckets — DefBuckets tops out at
	// 10 MiB/s and saturates almost immediately on real hardware.
	imagePullThroughput prom.Histogram
	// imagePullThroughputMiBps observes one MiB/s sample per image pull (not
	// per layer): bytes actually fetched from the registry over end-to-end
	// pull duration (includes extraction). Fully-cached pulls are skipped.
	imagePullThroughputMiBps prom.Histogram
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
			Help:      "Deprecated: use image_pulling_throughput_mibps instead. Image pull throughput in MiB/s (fetched bytes / pull duration; cached layers excluded).",
			Buckets:   prom.DefBuckets,
		},
	)
	imagePullThroughputMiBps = prom.NewHistogram(
		prom.HistogramOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "image_pulling_throughput_mibps",
			Help:      "image pull throughput in MiB/s (fetched bytes / pull duration; cached layers excluded)",
			// Buckets cover everything from slow registry pulls (0.1 MiB/s)
			// to 4000 MiB/s (~31 Gbps), enough for modern 10G+ NICs. The
			// top end is intentionally generous: past ~4 GiB/s, disk write
			// throughput becomes the bottleneck rather than the network, so
			// we don't need finer buckets above that.
			Buckets: []float64{0.1, 1, 10, 25, 75, 150, 300, 500, 1000, 2000, 4000},
		},
	)
	ns.Add(imagePullThroughput)
	ns.Add(imagePullThroughputMiBps)
	ns.Add(prom.NewGaugeFunc(prom.GaugeOpts{Namespace: namespace, Subsystem: subsystem, Name: "security_profiles", Help: "Number of stored security profiles"}, func() float64 { return securityProfileUsage(false) }))
	ns.Add(prom.NewGaugeFunc(prom.GaugeOpts{Namespace: namespace, Subsystem: subsystem, Name: "security_profiles_bytes", Help: "Bytes of stored security profiles"}, func() float64 { return securityProfileUsage(true) }))
	metrics.Register(ns)
}

// recordImagePullThroughput observes one MiB/s sample on obs (intended for
// imagePullThroughputMiBps). bytesPulled is bytes actually fetched from the
// registry — cached layers never trigger a fetch, so they are excluded.
// duration is the end-to-end pull duration (includes extraction, not just
// network transfer).
//
// Pulls that fetched nothing (fully cached) are skipped rather than recorded
// as zero or near-infinite samples, which would pollute the histogram.
func recordImagePullThroughput(obs prom.Observer, bytesPulled uint64, duration time.Duration) {
	if bytesPulled == 0 || duration <= 0 {
		return
	}
	obs.Observe(float64(bytesPulled) / mibToByte / duration.Seconds())
}

func securityProfileUsage(bytes bool) float64 {
	store := securityProfileMetricsStore.Load()
	if store == nil {
		return 0
	}
	count, size := store.Usage()
	if bytes {
		return float64(size)
	}
	return float64(count)
}
