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

package tracing

import (
	"context"

	srvconfig "github.com/containerd/containerd/services/server/config"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
)

// InitOpenTelemetry reads config and initializes otel middleware, sets the exporter
// propagator and global tracer provider
func InitOpenTelemetry(config *srvconfig.Config) (func(), error) {
	ctx := context.Background()

	// Check if tracing is configured
	if config.OpenTelemetry == (srvconfig.OpenTelemetryConfig{}) {
		logrus.Info("OpenTelemetry configuration not found, tracing is disabled")
		return nil, nil
	}

	// Validate configuration
	if err := config.OpenTelemetry.Validate(); err != nil {
		return nil, errors.Wrap(err, "invalid open telemetry configuration")
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			// Service name used to displace traces in backends
			semconv.ServiceNameKey.String(config.OpenTelemetry.ServiceName),
		),
	)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create resource")
	}

	// Configure OTLP trace exporter and set it up to connect to OpenTelemetry collector
	// running on a local host.
	ctrdTraceExporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(config.OpenTelemetry.ExporterEndpoint),
		otlptracegrpc.WithDialOption(grpc.WithBlock()),
	)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create trace exporter")
	}

	// Register the trace exporter with a TracerProvider, using a batch span
	// process to aggregate spans before export.
	ctrdBatchSpanProcessor := sdktrace.NewBatchSpanProcessor(ctrdTraceExporter)
	ctrdTracerProvider := sdktrace.NewTracerProvider(
		// We use TraceIDRatioBased sampling. Ratio read from config translated into following
		// if sampling ratio < 0 it is interpreted as 0. If ratio >= 1, it will always sample.
		sdktrace.WithSampler(sdktrace.TraceIDRatioBased(config.OpenTelemetry.TraceSamplingRatio)),
		sdktrace.WithResource(res),
		sdktrace.WithSpanProcessor(ctrdBatchSpanProcessor),
	)
	otel.SetTracerProvider(ctrdTracerProvider)

	// set global propagator to tracecontext
	otel.SetTextMapPropagator(propagation.TraceContext{})

	return func() {
		// Shutdown will flush any remaining spans and shut down the exporter.
		err := ctrdTracerProvider.Shutdown(ctx)
		if err != nil {
			logrus.WithError(err).Errorf("failed to shutdown TracerProvider")
		}
	}, nil
}

// StartSpan starts child span in a context.
func StartSpan(ctx context.Context, opName string, opts ...trace.SpanStartOption) (trace.Span, context.Context) {
	parentSpan := trace.SpanFromContext(ctx)
	tracer := trace.NewNoopTracerProvider().Tracer("")
	if parentSpan.SpanContext().IsValid() {
		tracer = parentSpan.TracerProvider().Tracer("")
	}
	ctx, span := tracer.Start(ctx, opName, opts...)
	return span, ctx
}

// StopSpan ends the span specified
func StopSpan(span trace.Span) {
	span.End()
}
