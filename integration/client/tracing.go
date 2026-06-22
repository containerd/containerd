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

//This file defines funcs to adding integration test around opentelemetry tracing by
// First, create tracer provider and in memory exporter to store generated spans from code.
// Then run the instrumented code where we expect spans.
// Then we check if the spans in exporter match expectation, like span name, status code and etc.

package client

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// newInMemoryExporterTracer creates in memory exporter and tracer provider to be
// used as tracing test
func newInMemoryExporterTracer() (*tracetest.InMemoryExporter, *sdktrace.TracerProvider) {
	//create in memory exporter
	exp := tracetest.NewInMemoryExporter()

	//create tracer provider
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
	)

	return exp, tp
}

// validateRootSpan takes span slice as input, check if there are rootspans match the expected
// name and the status code is not error
func validateRootSpan(t *testing.T, spanNameExpected string, spans []tracetest.SpanStub) {
	for _, span := range spans {
		//We only look for root span
		//A span is root span if its parent SpanContext is invalid
		if !span.Parent.IsValid() {
			if span.Name == spanNameExpected {
				assert.NotEqual(t, span.Status.Code, codes.Error)
				return
			}
		}
	}
	t.Fatalf("Expected span %s not found", spanNameExpected)
}
