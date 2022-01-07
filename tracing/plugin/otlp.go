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
	"fmt"
	"io"

	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/plugin"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
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
				return nil, fmt.Errorf("otlp endpoint not set: %w", plugin.ErrSkipPlugin)
			}
			dialOpts := []grpc.DialOption{grpc.WithBlock()}
			if cfg.Insecure {
				dialOpts = append(dialOpts, grpc.WithTransportCredentials(insecure.NewCredentials()))
			}

			exp, err := otlptracegrpc.New(ic.Context,
				otlptracegrpc.WithEndpoint(cfg.Endpoint),
				otlptracegrpc.WithDialOption(dialOpts...),
			)
			if err != nil {
				return nil, fmt.Errorf("failed to create otlp exporter: %w", err)
			}
			return sdktrace.NewBatchSpanProcessor(exp), nil
		},
	})
	plugin.Register(&plugin.Registration{
		ID:       "tracing",
		Type:     plugin.InternalPlugin,
		Requires: []plugin.Type{plugin.TracingProcessorPlugin},
		Config:   &TraceConfig{ServiceName: "containerd"},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			return newTracer(ic)
		},
	})
}

// OTLPConfig holds the configurations for the built-in otlp span processor
type OTLPConfig struct {
	Endpoint string `toml:"endpoint"`
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

// InitOpenTelemetry reads config and initializes otel middleware, sets the exporter
// propagator and global tracer provider
func newTracer(ic *plugin.InitContext) (io.Closer, error) {
	ctx := ic.Context
	config := ic.Config.(*TraceConfig)

	res, err := resource.New(ctx,
		resource.WithAttributes(
			// Service name used to displace traces in backends
			semconv.ServiceNameKey.String(config.ServiceName),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	opts := []sdktrace.TracerProviderOption{
		sdktrace.WithSampler(sdktrace.TraceIDRatioBased(config.TraceSamplingRatio)),
		sdktrace.WithResource(res),
	}

	ls, err := ic.GetByType(plugin.TracingProcessorPlugin)
	if err != nil {
		return nil, fmt.Errorf("failed to get tracing processors: %w", err)
	}

	procs := make([]sdktrace.SpanProcessor, 0, len(ls))
	for id, pctx := range ls {
		p, err := pctx.Instance()
		if err != nil {
			log.G(ctx).WithError(err).Errorf("Failed to init tracing processor %q", id)
			continue
		}
		proc := p.(sdktrace.SpanProcessor)
		opts = append(opts, sdktrace.WithSpanProcessor(proc))
		procs = append(procs, proc)
	}

	provider := sdktrace.NewTracerProvider(opts...)

	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	return &closer{close: func() error {
		for _, p := range procs {
			if err := p.Shutdown(ctx); err != nil {
				return err
			}
		}
		return nil
	}}, nil
}
