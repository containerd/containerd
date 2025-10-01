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
	"net/http"
	"strings"
	"sync"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/containerd/containerd/v2/pkg/tracing/common"
	"github.com/containerd/containerd/v2/pkg/tracing/enhanced"
	"github.com/containerd/containerd/v2/pkg/tracing/manager"
)

// globalTraceManager holds the global trace manager instance
var (
	globalTraceManager manager.Manager
	globalTraceMutex   sync.RWMutex
)

// sandboxIDResolver is an optional global hook that can derive a sandboxID
// from span attributes (e.g. via containerID -> sandboxID lookup).
// It should return (sandboxID, true) if resolved, otherwise ("", false).
var sandboxIDResolver func(attrs map[string]interface{}) (string, bool)

type sandboxIDKey struct{}

func ContextWithSandboxID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, sandboxIDKey{}, id)
}

// SetGlobalTraceManager sets the global trace manager for enhanced tracing
func SetGlobalTraceManager(tm manager.Manager) {
	globalTraceMutex.Lock()
	defer globalTraceMutex.Unlock()
	globalTraceManager = tm
}

// getGlobalTraceManager returns the global trace manager instance
func getGlobalTraceManager() manager.Manager {
	globalTraceMutex.RLock()
	defer globalTraceMutex.RUnlock()
	return globalTraceManager
}

// SetSandboxIDResolver installs a process-wide resolver used by enhanced tracer
// to populate sandbox.id when it is missing on a span.
func SetSandboxIDResolver(fn func(map[string]interface{}) (string, bool)) {
	globalTraceMutex.Lock()
	defer globalTraceMutex.Unlock()
	sandboxIDResolver = fn
}

func getSandboxIDResolver() func(map[string]interface{}) (string, bool) {
	globalTraceMutex.RLock()
	defer globalTraceMutex.RUnlock()
	return sandboxIDResolver
}

// isEnhancedTracingEnabled checks if enhanced tracing is enabled and available
func isEnhancedTracingEnabled() bool {
	tm := getGlobalTraceManager()
	if tm == nil {
		return false
	}
	return tm.IsEnabled()
}

// StartConfig defines configuration for a new span object.
type StartConfig struct {
	spanOpts []trace.SpanStartOption
}

// SpanOpt is an interface to add an option to StartConfig.
type SpanOpt func(*StartConfig)

// WithAttributes adds provided k/v attributes to a new span that is created.
// This is a convenience helper to avoid repeatedly using trace.WithAttributes(Attribute(k, v))
func WithAttributes(kv ...attribute.KeyValue) SpanOpt {
	return func(cfg *StartConfig) {
		cfg.spanOpts = append(cfg.spanOpts, trace.WithAttributes(kv...))
	}
}

// WithAttribute is a single key/value convenience wrapper kept for backward compatibility.
func WithAttribute(k string, v any) SpanOpt {
	return WithAttributes(Attribute(k, v))
}

// WithSpanOptions creates a SpanOpt to set trace.SpanStartOption(s).
func WithSpanOptions(opts ...trace.SpanStartOption) SpanOpt {
	return func(cfg *StartConfig) {
		cfg.spanOpts = append(cfg.spanOpts, opts...)
	}
}

// StartSpan creates a new Span and adds it to the context.
// If this function is called inside the context of a parent span, its return
// context holds the newly created span. Otherwise, it creates a span with implicit
// parent referencing and returns the context holding the new span.
// StartSpan creates a new Span and adds it to the context.
// If this function is called inside the context of a parent span, its return
// context holds the newly created span. Otherwise, it creates a span with implicit
// parent referencing and returns the context holding the new span.
func StartSpan(ctx context.Context, opName string, opts ...SpanOpt) (context.Context, *Span) {
	config := StartConfig{}
	for _, fn := range opts {
		fn(&config)
	}

	tracer := otel.Tracer("")
	if parent := trace.SpanFromContext(ctx); parent != nil && parent.SpanContext().IsValid() {
		tracer = parent.TracerProvider().Tracer("")
	}

	// Create the OpenTelemetry span as before
	ctx, otelSpan := tracer.Start(ctx, opName, config.spanOpts...)

	// Create wrapper span that handles both OTEL and enhanced tracing
	wrapperSpan := &Span{
		otelSpan: otelSpan,
	}

	// If enhanced tracing is enabled, create enhanced span in background
	if isEnhancedTracingEnabled() {
		tm := getGlobalTraceManager()
		enhancedSpan, enhancedCtx := enhanced.StartSpanFromOTEL(ctx, tm.GetTracer(), opName, otelSpan)
		if enhancedSpan != nil {
			wrapperSpan.enhancedSpan = enhancedSpan
			ctx = enhancedCtx
		}

		// Apply sandbox ID resolver if span lacks sandbox.id
		if resolver := getSandboxIDResolver(); resolver != nil && enhancedSpan != nil {
			if esp, ok := enhancedSpan.(*enhanced.EnhancedSpan); ok {
				if _, exists := esp.GetAttribute("sandbox.id"); !exists {
					if sid, ok := resolver(map[string]interface{}{
						"trace.name": opName,
					}); ok && sid != "" {
						esp.SetAttribute("sandbox.id", sid)
					}
				}
			}
		}

	}

	return ctx, wrapperSpan
}

// SpanFromContext returns the current Span from the context.
func SpanFromContext(ctx context.Context) *Span {
	otelSpan := trace.SpanFromContext(ctx)
	if otelSpan == nil {
		return nil
	}

	wrapperSpan := &Span{
		otelSpan: otelSpan,
	}

	// If enhanced tracing is enabled, try to get enhanced span from context
	if isEnhancedTracingEnabled() {
		if enhancedSpan := enhanced.SpanFromContext(ctx); enhancedSpan != nil {
			wrapperSpan.enhancedSpan = enhancedSpan
		}
	}

	return wrapperSpan
}

// Span is wrapper around both otel trace.Span and enhanced span.
type Span struct {
	otelSpan     trace.Span
	enhancedSpan enhanced.Span
}

// End completes both OTEL and enhanced spans.
func (s *Span) End() {
	if s.otelSpan != nil {
		s.otelSpan.End()
	}
	if s.enhancedSpan != nil {
		s.enhancedSpan.End()
	}
}

// AddEvent adds an event to both OTEL and enhanced spans.
func (s *Span) AddEvent(name string, attributes ...attribute.KeyValue) {
	if s.otelSpan != nil {
		s.otelSpan.AddEvent(name, trace.WithAttributes(attributes...))
	}
	if s.enhancedSpan != nil {
		enhancedAttrs := convertToEnhancedAttributes(attributes)
		s.enhancedSpan.AddEvent(name, enhancedAttrs...)
	}
}

// RecordError will record err as an exception span event for this span
func (s *Span) RecordError(err error, options ...trace.EventOption) {
	if s.otelSpan != nil {
		s.otelSpan.RecordError(err, options...)
	}
	if s.enhancedSpan != nil {
		s.enhancedSpan.RecordError(err)
	}
}

// SetStatus sets the status of both OTEL and enhanced spans.
func (s *Span) SetStatus(err error) {
	if s.otelSpan != nil {
		if err != nil {
			s.otelSpan.RecordError(err)
			s.otelSpan.SetStatus(codes.Error, err.Error())
		} else {
			s.otelSpan.SetStatus(codes.Ok, "")
		}
	}
	if s.enhancedSpan != nil {
		if err != nil {
			s.enhancedSpan.SetStatus(enhanced.StatusCodeError, err.Error())
			s.enhancedSpan.RecordError(err)
		} else {
			s.enhancedSpan.SetStatus(enhanced.StatusCodeOk, "success")
		}
	}
}

// SetAttributes sets attributes on both OTEL and enhanced spans.
func (s *Span) SetAttributes(kv ...attribute.KeyValue) {
	if s.otelSpan != nil {
		s.otelSpan.SetAttributes(kv...)
	}
	if s.enhancedSpan != nil {
		enhancedAttrs := convertToEnhancedAttributes(kv)
		s.enhancedSpan.SetAttributes(enhancedAttrs...)
	}
}

// SetAttribute sets a single attribute on both OTEL and enhanced spans.
func (s *Span) SetAttribute(key string, value interface{}) {
	if s.otelSpan != nil {
		s.otelSpan.SetAttributes(Attribute(key, value))
	}
	if s.enhancedSpan != nil {
		s.enhancedSpan.SetAttribute(key, value)
	}
}

const spanDelimiter = "."

// Name sets the span name by joining a list of strings in dot separated format.
func Name(parts ...string) string {
	return strings.Join(parts, spanDelimiter)
}

// SpanOperation returns a composite span operation name.
func SpanOperation(parts ...string) string {
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, spanDelimiter)
}

// Attribute returns generic attribute.KeyValue type for any supported input types.
func Attribute(k string, v any) attribute.KeyValue {
	return common.KeyValue(k, v)
}

// HTTPStatusCodeAttributes returns the attributes list for the given status code.
func HTTPStatusCodeAttributes(code int) []attribute.KeyValue {
	return []attribute.KeyValue{semconv.HTTPStatusCodeKey.Int(code)}
}

// UpdateHTTPClient wraps the given http.Client's transport with otelhttp instrumentation
// and returns a new client instance to avoid modifying the original.
func UpdateHTTPClient(client *http.Client, _ ...string) *http.Client {
	if client == nil {
		return http.DefaultClient
	}

	// Copy to avoid side-effects
	newClient := *client

	rt := newClient.Transport
	if rt == nil {
		rt = http.DefaultTransport
	}

	newClient.Transport = otelhttp.NewTransport(rt)
	return &newClient
}

// Helper function to convert OTEL attributes to enhanced attributes
func convertToEnhancedAttributes(otelAttrs []attribute.KeyValue) []enhanced.Attribute {
	var enhancedAttrs []enhanced.Attribute
	for _, attr := range otelAttrs {
		enhancedAttrs = append(enhancedAttrs, enhanced.Attribute{
			Key:   string(attr.Key),
			Value: attr.Value.AsInterface(),
		})
	}
	return enhancedAttrs
}

// EnhancedSpan returns the underlying enhanced span if available
func (s *Span) EnhancedSpan() enhanced.Span {
	return s.enhancedSpan
}

// IsEnhancedTracingActive returns true if enhanced tracing is active
func (s *Span) IsEnhancedTracingActive() bool {
	return s.enhancedSpan != nil
}

// IsRecording reports whether either the underlying OTEL span or the enhanced span is still recording.
func (s *Span) IsRecording() bool {
	if s == nil {
		return false
	}
	if s.otelSpan != nil && s.otelSpan.IsRecording() {
		return true
	}
	if s.enhancedSpan != nil && s.enhancedSpan.IsRecording() {
		return true
	}
	return false
}
