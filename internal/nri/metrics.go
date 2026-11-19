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

package nri

import (
	"context"
	"errors"
	"time"

	"github.com/docker/go-metrics"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	nri "github.com/containerd/nri/pkg/adaptation"
)

var (
	nriPluginInvocations metrics.LabeledCounter
	nriPluginLatency     metrics.LabeledTimer
	nriPluginAdjustments metrics.LabeledCounter
	nriActivePlugins     metrics.Gauge
)

func init() {
	ns := metrics.NewNamespace("containerd", "nri", nil)

	nriPluginInvocations = ns.NewLabeledCounter("plugin_invocations", "Number of NRI plugin invocations", "plugin", "operation", "status", "error")
	nriPluginAdjustments = ns.NewLabeledCounter("plugin_adjustments", "Number of adjustment operations from plugins", "plugin", "operation", "type")
	nriActivePlugins = ns.NewGauge("active_plugins", "Number of currently active NRI plugins", metrics.Total)
	nriPluginLatency = ns.NewLabeledTimer("plugin_latency", "NRI plugin operation latency", "plugin", "operation")

	metrics.Register(ns)
}

type nriMetrics struct{}

var _ nri.Metrics = (*nriMetrics)(nil)

func newNRIMetrics() nri.Metrics {
	return &nriMetrics{}
}

func (m *nriMetrics) RecordPluginInvocation(pluginName, operation string, err error) {
	opStatus := "success"
	errorType := ""
	if err != nil {
		opStatus = "error"
		errorType = getErrorType(err)
	}
	nriPluginInvocations.WithValues(pluginName, operation, opStatus, errorType).Inc()
}

// Prometheus conventions recommend lowercase snake_case for unified label values.
// Avoids the UpperCamelCase strings returned by gRPC's Code.String() (e.g., "InvalidArgument").
var grpcCodeToSnake = map[codes.Code]string{
	codes.OK:                 "ok",
	codes.Canceled:           "canceled",
	codes.Unknown:            "unknown",
	codes.InvalidArgument:    "invalid_argument",
	codes.DeadlineExceeded:   "deadline_exceeded",
	codes.NotFound:           "not_found",
	codes.AlreadyExists:      "already_exists",
	codes.PermissionDenied:   "permission_denied",
	codes.ResourceExhausted:  "resource_exhausted",
	codes.FailedPrecondition: "failed_precondition",
	codes.Aborted:            "aborted",
	codes.OutOfRange:         "out_of_range",
	codes.Unimplemented:      "unimplemented",
	codes.Internal:           "internal",
	codes.Unavailable:        "unavailable",
	codes.DataLoss:           "data_loss",
	codes.Unauthenticated:    "unauthenticated",
}

func getErrorType(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline_exceeded"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	if st, ok := status.FromError(err); ok {
		if s, found := grpcCodeToSnake[st.Code()]; found {
			return s
		}
		return "unknown_code"
	}
	return "other"
}

func (m *nriMetrics) RecordPluginLatency(pluginName, operation string, latency time.Duration) {
	nriPluginLatency.WithValues(pluginName, operation).Update(latency)
}

func (m *nriMetrics) RecordPluginAdjustments(pluginName, operation string, _ *nri.ContainerAdjustment, updates, evicts int) {
	if updates > 0 {
		nriPluginAdjustments.WithValues(pluginName, operation, "update").Inc(float64(updates))
	}
	if evicts > 0 {
		nriPluginAdjustments.WithValues(pluginName, operation, "evict").Inc(float64(evicts))
	}
}

func (m *nriMetrics) UpdatePluginCount(count int) {
	nriActivePlugins.Set(float64(count))
}
