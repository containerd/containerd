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
	"errors"
	"testing"

	"github.com/containerd/containerd/errdefs"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// TestNewExporter runs tests with different combinations of configuration for NewExporter function
func TestNewExporter(t *testing.T) {

	for _, testcase := range []struct {
		name   string
		input  OTLPConfig
		output error
	}{
		{
			name: "Test http/protobuf protocol, expect no error",
			input: OTLPConfig{Endpoint: "http://localhost:4318",
				Protocol: "http/protobuf",
				Insecure: false},
			output: nil,
		},
		{
			name: "Test invalid endpoint, expect ErrInvalidArgument error",
			input: OTLPConfig{Endpoint: "http://localhost\n:4318",
				Protocol: "http/protobuf",
				Insecure: false},
			output: errdefs.ErrInvalidArgument,
		},
		{
			name: "Test default protocol, expect no error",
			input: OTLPConfig{Endpoint: "http://localhost:4318",
				Protocol: "",
				Insecure: false},
			output: nil,
		},
		{
			name: "Test grpc protocol, expect no error",
			input: OTLPConfig{Endpoint: "http://localhost:4317",
				Protocol: "grpc",
				Insecure: false},
			output: nil,
		},
		{
			name: "Test http/json protocol which is not supported, expect not implemented error",
			input: OTLPConfig{Endpoint: "http://localhost:4318",
				Protocol: "http/json",
				Insecure: false},
			output: errdefs.ErrNotImplemented,
		},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			t.Logf("input: %v", testcase.input)

			ctx := context.TODO()
			exp, err := newExporter(ctx, &testcase.input)
			t.Logf("output: %v", err)

			if err == nil {
				if err != testcase.output {
					t.Fatalf("Expect to get error: %v, however no error got\n", testcase.output)
				} else if exp == nil {
					t.Fatalf("Something went wrong, Exporter not created as expected\n")
				}
			} else {
				if !errors.Is(err, testcase.output) {
					t.Fatalf("Expect to get error: %v, however error %v returned\n", testcase.output, err)
				}
			}

		})
	}
}

// TestNewTracer runs test for NewTracer function
func TestNewTracer(t *testing.T) {

	config := &TraceConfig{ServiceName: "containerd", TraceSamplingRatio: 1.0}
	t.Logf("config: %v", config)

	procs := make([]trace.SpanProcessor, 0, 1)

	//Create a dummy in memory exporter for test
	exp := tracetest.NewInMemoryExporter()
	proc := trace.NewBatchSpanProcessor(exp)

	procs = append(procs, proc)

	ctx := context.TODO()
	tracerCloser, err := newTracer(ctx, config, procs)
	if err != nil {
		t.Fatalf("Something went wrong, Tracer not created as expected\n")
	}

	defer tracerCloser.Close()
}
