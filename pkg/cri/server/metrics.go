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
	"github.com/docker/go-metrics"
)

type collector struct {
	networkSetupTotal   metrics.Counter
	networkSetupErrors  metrics.Counter
	networkSetupLatency metrics.Timer

	networkTeardownTotal   metrics.Counter
	networkTeardownErrors  metrics.Counter
	networkTeardownLatency metrics.Timer
}

func (c *criService) configureMetrics(ns *metrics.Namespace) {
	c.metrics.networkSetupTotal = ns.NewCounter("network_setup", "Total number of network setup attempts")
	c.metrics.networkSetupErrors = ns.NewCounter("network_setup_errors", "Count of errors setting up networks")
	c.metrics.networkSetupLatency = ns.NewTimer("network_setup_latency", "Latency of network setup attempts")

	c.metrics.networkTeardownTotal = ns.NewCounter("network_teardown", "Total number of network teardown attempts")
	c.metrics.networkTeardownErrors = ns.NewCounter("network_teardown_errors", "Count of errors tearing down networks")
	c.metrics.networkTeardownLatency = ns.NewTimer("network_teardown_latency", "Latency of network teardown attempts")

	metrics.Register(ns)
}
