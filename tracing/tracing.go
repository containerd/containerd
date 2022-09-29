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

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// StartSpan starts child span in a context.
func StartSpan(ctx context.Context, opName string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	if parent := trace.SpanFromContext(ctx); parent != nil && parent.SpanContext().IsValid() {
		return parent.TracerProvider().Tracer("").Start(ctx, opName, opts...)
	}
	return otel.Tracer("").Start(ctx, opName, opts...)
}

// StopSpan ends the span specified
func StopSpan(span trace.Span) {
	span.End()
}

// CurrentSpan returns current span from context or noopSpan if no span exists.
func CurrentSpan(ctx context.Context) trace.Span {
	return trace.SpanFromContext(ctx)
}

// SetSpanStatus sets the status of the current span.
// If an error is encountered, it records the error and sets span status to Error.
func SetSpanStatus(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	} else {
		span.SetStatus(codes.Ok, "")
	}
}
