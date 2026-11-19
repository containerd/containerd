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

package grpc

import (
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"
	grpc_prometheus "github.com/grpc-ecosystem/go-grpc-middleware/providers/prometheus"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"

	"github.com/containerd/containerd/v2/plugins"
)

type metricsConfig struct {
	GRPCHistogram bool `toml:"grpc_histogram"`
}

func init() {
	registry.Register(&plugin.Registration{
		Type: plugins.MetricsPlugin,
		ID:   "grpc-prometheus",
		Requires: []plugin.Type{
			plugins.GRPCPlugin,
		},
		Config: &metricsConfig{
			GRPCHistogram: false,
		},
		InitFn: func(ic *plugin.InitContext) (any, error) {
			c := ic.Config.(*metricsConfig)
			var prometheusServerMetricsOpts []grpc_prometheus.ServerMetricsOption
			if c.GRPCHistogram {
				// Enable grpc time histograms to measure rpc latencies
				prometheusServerMetricsOpts = append(prometheusServerMetricsOpts, grpc_prometheus.WithServerHandlingTimeHistogram())
			}

			prometheusServerMetrics := grpc_prometheus.NewServerMetrics(prometheusServerMetricsOpts...)
			prometheus.MustRegister(prometheusServerMetrics)

			return prometheusServerMetrics, nil
		},
	})
	registry.Register(&plugin.Registration{
		Type: plugins.MetricsPlugin,
		ID:   "grpc-otel",
		Requires: []plugin.Type{
			plugins.GRPCPlugin,
		},
		InitFn: func(ic *plugin.InitContext) (any, error) {
			return otelgrpc.NewServerHandler(), nil
		},
	})
}
