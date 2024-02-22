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

package plugin

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/containerd/containerd/v2/pkg/deprecation"
	"github.com/containerd/containerd/v2/pkg/tracing"
	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/containerd/v2/plugins/services/warning"
	"github.com/containerd/errdefs"
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/trace"
)

const exporterPlugin = "otlp"

// OTEL and OTLP standard env vars
// See https://opentelemetry.io/docs/specs/otel/configuration/sdk-environment-variables/
const (
	sdkDisabledEnv = "OTEL_SDK_DISABLED"

	otlpEndpointEnv       = "OTEL_EXPORTER_OTLP_ENDPOINT"
	otlpTracesEndpointEnv = "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"
	otlpProtocolEnv       = "OTEL_EXPORTER_OTLP_PROTOCOL"
	otlpTracesProtocolEnv = "OTEL_EXPORTER_OTLP_TRACES_PROTOCOL"

	otelTracesExporterEnv = "OTEL_TRACES_EXPORTER"
	otelServiceNameEnv    = "OTEL_SERVICE_NAME"
)

func init() {
	registry.Register(&plugin.Registration{
		ID:     exporterPlugin,
		Type:   plugins.TracingProcessorPlugin,
		Config: &OTLPConfig{},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			if err := warnOTLPConfig(ic); err != nil {
				return nil, err
			}
			if err := checkDisabled(); err != nil {
				return nil, err
			}

			// If OTEL_TRACES_EXPORTER is set, it must be "otlp"
			if v := os.Getenv(otelTracesExporterEnv); v != "" && v != "otlp" {
				return nil, fmt.Errorf("unsupported traces exporter %q: %w", v, errdefs.ErrInvalidArgument)
			}

			exp, err := newExporter(ic.Context)
			if err != nil {
				return nil, err
			}
			return trace.NewBatchSpanProcessor(exp), nil
		},
	})
	registry.Register(&plugin.Registration{
		ID:     "tracing",
		Type:   plugins.InternalPlugin,
		Config: &TraceConfig{},
		Requires: []plugin.Type{
			plugins.TracingProcessorPlugin,
		},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			if err := warnTraceConfig(ic); err != nil {
				return nil, err
			}
			if err := checkDisabled(); err != nil {
				return nil, err
			}

			//get TracingProcessorPlugin which is a dependency
			plugins, err := ic.GetByType(plugins.TracingProcessorPlugin)
			if err != nil {
				return nil, fmt.Errorf("failed to get tracing processors: %w", err)
			}

			procs := make([]trace.SpanProcessor, 0, len(plugins))
			for _, p := range plugins {
				procs = append(procs, p.(trace.SpanProcessor))
			}

			return newTracer(ic.Context, procs)
		},
	})

	// Register logging hook for tracing
	logrus.StandardLogger().AddHook(tracing.NewLogrusHook())
}

// OTLPConfig holds the configurations for the built-in otlp span processor
type OTLPConfig struct {
	Endpoint string `toml:"endpoint,omitempty"`
	Protocol string `toml:"protocol,omitempty"`
	Insecure bool   `toml:"insecure,omitempty"`
}

// TraceConfig is the common configuration for open telemetry.
type TraceConfig struct {
	ServiceName        string  `toml:"service_name,omitempty"`
	TraceSamplingRatio float64 `toml:"sampling_ratio,omitempty"`
}

func checkDisabled() error {
	v := os.Getenv(sdkDisabledEnv)
	if v != "" {
		disable, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("invalid value for %s: %w: %w", sdkDisabledEnv, err, errdefs.ErrInvalidArgument)
		}
		if disable {
			return fmt.Errorf("%w: tracing disabled by env %s=%s", plugin.ErrSkipPlugin, sdkDisabledEnv, v)
		}
	}

	if os.Getenv(otlpEndpointEnv) == "" && os.Getenv(otlpTracesEndpointEnv) == "" {
		return fmt.Errorf("%w: tracing endpoint not configured", plugin.ErrSkipPlugin)
	}
	return nil
}

type closerFunc func() error

func (f closerFunc) Close() error {
	return f()
}

// newExporter creates an exporter based on the given configuration.
//
// The default protocol is http/protobuf since it is recommended by
// https://github.com/open-telemetry/opentelemetry-specification/blob/v1.8.0/specification/protocol/exporter.md#specify-protocol.
func newExporter(ctx context.Context) (*otlptrace.Exporter, error) {
	const timeout = 5 * time.Second

	v := os.Getenv(otlpTracesProtocolEnv)
	if v == "" {
		v = os.Getenv(otlpProtocolEnv)
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	switch v {
	case "", "http/protobuf":
		return otlptracehttp.New(ctx)
	case "grpc":
		return otlptracegrpc.New(ctx)
	default:
		// Other protocols such as "http/json" are not supported.
		return nil, fmt.Errorf("OpenTelemetry protocol %q : %w", v, errdefs.ErrNotImplemented)
	}
}

// newTracer configures protocol-agonostic tracing settings such as
// its sampling ratio and returns io.Closer.
//
// Note that this function sets process-wide tracing configuration.
func newTracer(ctx context.Context, procs []trace.SpanProcessor) (io.Closer, error) {
	// Let otel configure the service name from env
	if os.Getenv(otelServiceNameEnv) == "" {
		os.Setenv(otelServiceNameEnv, "containerd")
	}

	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))

	opts := make([]trace.TracerProviderOption, 0, len(procs))
	for _, proc := range procs {
		opts = append(opts, trace.WithSpanProcessor(proc))
	}
	provider := trace.NewTracerProvider(opts...)
	otel.SetTracerProvider(provider)

	return closerFunc(func() error {
		return provider.Shutdown(ctx)
	}), nil

}

func warnTraceConfig(ic *plugin.InitContext) error {
	ctx := ic.Context
	cfg := ic.Config.(*TraceConfig)
	var warn bool
	if cfg.ServiceName != "" {
		warn = true
	}
	if cfg.TraceSamplingRatio != 0 {
		warn = true
	}

	if !warn {
		return nil
	}

	wp, err := ic.GetSingle(plugins.WarningPlugin)
	if err != nil {
		return err
	}
	ws := wp.(warning.Service)
	ws.Emit(ctx, deprecation.TracingServiceConfig)
	return nil
}

func warnOTLPConfig(ic *plugin.InitContext) error {
	ctx := ic.Context
	cfg := ic.Config.(*OTLPConfig)
	var warn bool
	if cfg.Endpoint != "" {
		warn = true
	}
	if cfg.Protocol != "" {
		warn = true
	}
	if cfg.Insecure {
		warn = true
	}

	if !warn {
		return nil
	}

	wp, err := ic.GetSingle(plugins.WarningPlugin)
	if err != nil {
		return err
	}
	ws := wp.(warning.Service)
	ws.Emit(ctx, deprecation.TracingOTLPConfig)
	return nil
}
