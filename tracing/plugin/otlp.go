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
	"net/url"
	"time"

	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/tracing"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
)

const exporterPlugin = "otlp"

func init() {
	plugin.Register(&plugin.Registration{
		ID:     exporterPlugin,
		Type:   plugin.TracingProcessorPlugin,
		Config: &OTLPConfig{},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			cfg := ic.Config.(*OTLPConfig)
			if cfg.Endpoint == "" {
				return nil, fmt.Errorf("no OpenTelemetry endpoint: %w", plugin.ErrSkipPlugin)
			}
			exp, err := newExporter(ic.Context, cfg)
			if err != nil {
				return nil, err
			}
			return trace.NewBatchSpanProcessor(exp), nil
		},
	})
	plugin.Register(&plugin.Registration{
		ID:       "tracing",
		Type:     plugin.InternalPlugin,
		Requires: []plugin.Type{plugin.TracingProcessorPlugin},
		Config:   &TraceConfig{ServiceName: "containerd", TraceSamplingRatio: 1.0},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			//get TracingProcessorPlugin which is a dependency
			plugins, err := ic.GetByType(plugin.TracingProcessorPlugin)
			if err != nil {
				return nil, fmt.Errorf("failed to get tracing processors: %w", err)
			}
			procs := make([]trace.SpanProcessor, 0, len(plugins))
			for id, pctx := range plugins {
				p, err := pctx.Instance()
				if err != nil {
					if plugin.IsSkipPlugin(err) {
						log.G(ic.Context).WithError(err).Infof("skipping tracing processor initialization (no tracing plugin)")
					} else {
						log.G(ic.Context).WithError(err).Errorf("failed to initialize a tracing processor %q", id)
					}
					continue
				}
				proc := p.(trace.SpanProcessor)
				procs = append(procs, proc)
			}
			return newTracer(ic.Context, ic.Config.(*TraceConfig), procs)
		},
	})

	// Register logging hook for tracing
	logrus.StandardLogger().AddHook(tracing.NewLogrusHook())
}

// OTLPConfig holds the configurations for the built-in otlp span processor
type OTLPConfig struct {
	Endpoint string `toml:"endpoint"`
	Protocol string `toml:"protocol"`
	Insecure bool   `toml:"insecure"`
}

// TraceConfig is the common configuration for open telemetry.
type TraceConfig struct {
	ServiceName        string  `toml:"service_name"`
	TraceSamplingRatio float64 `toml:"sampling_ratio"`
}

type closer struct {
	close func() error
}

func (c *closer) Close() error {
	return c.close()
}

// newExporter creates an exporter based on the given configuration.
//
// The default protocol is http/protobuf since it is recommended by
// https://github.com/open-telemetry/opentelemetry-specification/blob/v1.8.0/specification/protocol/exporter.md#specify-protocol.
func newExporter(ctx context.Context, cfg *OTLPConfig) (*otlptrace.Exporter, error) {
	const timeout = 5 * time.Second

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if cfg.Protocol == "http/protobuf" || cfg.Protocol == "" {
		u, err := url.Parse(cfg.Endpoint)
		if err != nil {
			return nil, fmt.Errorf("OpenTelemetry endpoint %q %w : %v", cfg.Endpoint, errdefs.ErrInvalidArgument, err)
		}
		opts := []otlptracehttp.Option{
			otlptracehttp.WithEndpoint(u.Host),
		}
		if u.Scheme == "http" {
			opts = append(opts, otlptracehttp.WithInsecure())
		}
		return otlptracehttp.New(ctx, opts...)
	} else if cfg.Protocol == "grpc" {
		opts := []otlptracegrpc.Option{
			otlptracegrpc.WithEndpoint(cfg.Endpoint),
		}
		if cfg.Insecure {
			opts = append(opts, otlptracegrpc.WithInsecure())
		}
		return otlptracegrpc.New(ctx, opts...)
	} else {
		// Other protocols such as "http/json" are not supported.
		return nil, fmt.Errorf("OpenTelemetry protocol %q : %w", cfg.Protocol, errdefs.ErrNotImplemented)
	}
}

// newTracer configures protocol-agonostic tracing settings such as
// its sampling ratio and returns io.Closer.
//
// Note that this function sets process-wide tracing configuration.
func newTracer(ctx context.Context, config *TraceConfig, procs []trace.SpanProcessor) (io.Closer, error) {

	res, err := resource.New(ctx,
		resource.WithHost(),
		resource.WithAttributes(
			// Service name used to displace traces in backends
			semconv.ServiceNameKey.String(config.ServiceName),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	sampler := trace.ParentBased(trace.TraceIDRatioBased(config.TraceSamplingRatio))

	opts := []trace.TracerProviderOption{
		trace.WithSampler(sampler),
		trace.WithResource(res),
	}

	for _, proc := range procs {
		opts = append(opts, trace.WithSpanProcessor(proc))
	}

	provider := trace.NewTracerProvider(opts...)

	otel.SetTracerProvider(provider)

	otel.SetTextMapPropagator(propagators())

	return &closer{close: func() error {
		for _, p := range procs {
			if err := p.Shutdown(ctx); err != nil {
				return err
			}
		}
		return nil
	}}, nil

}

// Returns a composite TestMap propagator
func propagators() propagation.TextMapPropagator {
	return propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{})
}
