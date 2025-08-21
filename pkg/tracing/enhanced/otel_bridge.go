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

package enhanced

import (
	"context"

	"go.opentelemetry.io/otel/trace"
)

// StartSpanFromOTEL creates an enhanced span from an existing OTEL span context
func StartSpanFromOTEL(ctx context.Context, tracer Tracer, opName string, otelSpan trace.Span) (Span, context.Context) {
	if tracer == nil || !tracer.IsEnabled() {
		return nil, ctx
	}
	var traceID string
	if otelSpan != nil && otelSpan.SpanContext().IsValid() {
		traceID = otelSpan.SpanContext().TraceID().String()
	}
	span, newCtx := tracer.StartSpan(ctx, opName, traceID)
	return span, newCtx
}

// SpanFromContext retrieves enhanced span from context
func SpanFromContext(ctx context.Context) Span {
	if span, ok := ctx.Value(enhancedSpanKey{}).(Span); ok {
		return span
	}
	return nil
}

// ContextWithSpan stores enhanced span in context
func ContextWithSpan(ctx context.Context, span Span) context.Context {
	return context.WithValue(ctx, enhancedSpanKey{}, span)
}

type enhancedSpanKey struct{}
