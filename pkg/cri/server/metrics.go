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

package server

import (
	metrics "github.com/docker/go-metrics"
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
)

func init() {
	// these CRI metrics record latencies for successful operations around a sandbox and container's lifecycle.
	ns := metrics.NewNamespace("containerd", "cri", nil)

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

	metrics.Register(ns)
}
