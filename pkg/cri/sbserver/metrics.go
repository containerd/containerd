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

package sbserver

import (
	"github.com/docker/go-metrics"
	prom "github.com/prometheus/client_golang/prometheus"
)

var (
	sandboxListTimer          metrics.Timer
	sandboxCreateNetworkTimer metrics.Timer
	sandboxDeleteNetwork      metrics.Timer

	sandboxRuntimeCreateTimer metrics.LabeledTimer
	sandboxRuntimeStopTimer   metrics.LabeledTimer
	sandboxRemoveTimer        metrics.LabeledTimer

	containerListTimer   metrics.Timer
	containerRemoveTimer metrics.LabeledTimer
	containerCreateTimer metrics.LabeledTimer
	containerStopTimer   metrics.LabeledTimer
	containerStartTimer  metrics.LabeledTimer

	networkPluginOperations        metrics.LabeledCounter
	networkPluginOperationsErrors  metrics.LabeledCounter
	networkPluginOperationsLatency metrics.LabeledTimer

	imagePulls           metrics.LabeledCounter
	inProgressImagePulls metrics.Gauge
	//  pull duration / (image size / 1MBi)
	imagePullThroughput prom.Histogram
)

func init() {
	// these CRI metrics record latencies for successful operations around a sandbox and container's lifecycle.
	ns := metrics.NewNamespace("containerd", "cri_sandboxed", nil)

	sandboxListTimer = ns.NewTimer("sandbox_list", "time to list sandboxes")
	sandboxCreateNetworkTimer = ns.NewTimer("sandbox_create_network", "time to create the network for a sandbox")
	sandboxDeleteNetwork = ns.NewTimer("sandbox_delete_network", "time to delete a sandbox's network")

	sandboxRuntimeCreateTimer = ns.NewLabeledTimer("sandbox_runtime_create", "time to create a sandbox in the runtime", "runtime")
	sandboxRuntimeStopTimer = ns.NewLabeledTimer("sandbox_runtime_stop", "time to stop a sandbox", "runtime")
	sandboxRemoveTimer = ns.NewLabeledTimer("sandbox_remove", "time to remove a sandbox", "runtime")

	containerListTimer = ns.NewTimer("container_list", "time to list containers")
	containerRemoveTimer = ns.NewLabeledTimer("container_remove", "time to remove a container", "runtime")
	containerCreateTimer = ns.NewLabeledTimer("container_create", "time to create a container", "runtime")
	containerStopTimer = ns.NewLabeledTimer("container_stop", "time to stop a container", "runtime")
	containerStartTimer = ns.NewLabeledTimer("container_start", "time to start a container", "runtime")

	networkPluginOperations = ns.NewLabeledCounter("network_plugin_operations_total", "cumulative number of network plugin operations by operation type", "operation_type")
	networkPluginOperationsErrors = ns.NewLabeledCounter("network_plugin_operations_errors_total", "cumulative number of network plugin operations by operation type", "operation_type")
	networkPluginOperationsLatency = ns.NewLabeledTimer("network_plugin_operations_duration_seconds", "latency in seconds of network plugin operations. Broken down by operation type", "operation_type")

	imagePulls = ns.NewLabeledCounter("image_pulls", "succeeded and failed counters", "status")
	inProgressImagePulls = ns.NewGauge("in_progress_image_pulls", "in progress pulls", metrics.Total)
	imagePullThroughput = prom.NewHistogram(
		prom.HistogramOpts{
			Name:    "image_pulling_throughput",
			Help:    "image pull throughput",
			Buckets: prom.DefBuckets,
		},
	)

	metrics.Register(ns)
}

// for backwards compatibility with kubelet/dockershim metrics
// https://github.com/containerd/containerd/issues/7801
const (
	networkStatusOp   = "get_pod_network_status"
	networkSetUpOp    = "set_up_pod"
	networkTearDownOp = "tear_down_pod"
)
