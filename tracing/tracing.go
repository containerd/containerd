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

// InitOpenTelemetry read config and initializes otel middleware, sets the exporter
// propagator and global tracer provider.
func InitOpenTelemetry(config *srvconfig.Config) (func(), error) {
	ctx := context.Background()

	// Check if tracing is configured
	if config.OpenTelemetry == *new(srvconfig.OpenTelemetryConfig) {
		logrus.Info("OpenTelemetry configuration is not found in config, tracing will be disabled")
		return nil, nil
	}

	// Validate configuration
	err := config.OpenTelemetry.Validate()
	if err != nil {
		return nil, errors.Wrap(err, "invalid open telemetry configuration")
	}

	otelCfg := config.OpenTelemetry

	// Currently we only support OTLP as exporter
	if otelCfg.ExporterName != "otlp" {
		return nil, errors.Wrapf(err, "Unsupported exporter %s", otelCfg.ExporterName)
	}

	// Configure OTLP exporter
	res, err := resource.New(ctx,
		resource.WithAttributes(
			// Service name used to displace traces in backends
			semconv.ServiceNameKey.String("containerd"),
		),
	)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create resource")
	}

	// Configure OTLP trace exporter and set it up to connect to OpenTelemetry collector
	// running on a local host
	ctrdTraceExporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithInsecure(),
		otlptracegrpc.WithEndpoint("localhost:4317"),
		otlptracegrpc.WithDialOption(grpc.WithBlock()),
	)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create trace exporter")
	}

	// Register the trace exporter with a TracerProvider, using a batch span
	// process to aggregate spans before export.
	ctrdBatchSpanProcessor := sdktrace.NewBatchSpanProcessor(ctrdTraceExporter)
	ctrdTracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.TraceIDRatioBased(0.5)),
		sdktrace.WithResource(res),
		sdktrace.WithSpanProcessor(ctrdBatchSpanProcessor),
	)
	otel.SetTracerProvider(ctrdTracerProvider)

	// set global propagator to tracecontext
	otel.SetTextMapPropagator(propagation.TraceContext{})

	return func() {
		// Shutdown will flush any remaining spans and shut down the exporter
		err := ctrdTracerProvider.Shutdown(ctx)
		if err != nil {
			logrus.Fatalf("failed to shutdown TracerProvider: %v", err)
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

// GetContextWithSpan sets the span of a context from another context if not set already
func GetContextWithSpan(ctx, ctxAnother context.Context) context.Context {
	if span := trace.SpanFromContext(ctx); span.SpanContext().IsValid() {
		return ctx
	}

	if span := trace.SpanFromContext(ctxAnother); span != nil {
		return trace.ContextWithSpan(ctx, span)
	}
	return ctx
}
