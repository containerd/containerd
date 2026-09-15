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

	"github.com/containerd/errdefs"
	"github.com/containerd/plugin"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestNewExporter(t *testing.T) {
	for _, tc := range []struct {
		name           string
		protocol       string
		tracesProtocol string
		output         error
	}{
		{
			name:     "Test http/protobuf protocol, expect no error",
			protocol: "http/protobuf",
			output:   nil,
		},
		{
			name:     "Test default protocol, expect no error",
			protocol: "",
			output:   nil,
		},
		{
			name:     "Test grpc protocol, expect no error",
			protocol: "grpc",
			output:   nil,
		},
		{
			name:     "Test http/json protocol which is not supported, expect not implemented error",
			protocol: "http/json",
			output:   errdefs.ErrNotImplemented,
		},
		{
			name:           "Test traces protocol takes precedence over protocol",
			protocol:       "http/json",
			tracesProtocol: "http/protobuf",
			output:         nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(otlpProtocolEnv, tc.protocol)
			t.Setenv(otlpTracesProtocolEnv, tc.tracesProtocol)

			ctx := context.TODO()
			exp, err := newExporter(ctx)
			if tc.output == nil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if exp == nil {
					t.Fatal("expected exporter to be created, got nil")
				}
			} else {
				if !errors.Is(err, tc.output) {
					t.Fatalf("expected error %v, got %v", tc.output, err)
				}
			}
		})
	}
}

func TestCheckDisabled(t *testing.T) {
	for _, tc := range []struct {
		name           string
		endpoint       string
		tracesEndpoint string
		sdkDisabled    string
		output         error
	}{
		{
			name:   "No endpoint configured, expect ErrSkipPlugin",
			output: plugin.ErrSkipPlugin,
		},
		{
			name:     "OTEL_EXPORTER_OTLP_ENDPOINT configured, expect no error",
			endpoint: "http://localhost:4318",
			output:   nil,
		},
		{
			name:           "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT configured, expect no error",
			tracesEndpoint: "http://localhost:4318",
			output:         nil,
		},
		{
			name:        "OTEL_SDK_DISABLED=true with endpoint, expect ErrSkipPlugin",
			endpoint:    "http://localhost:4318",
			sdkDisabled: "true",
			output:      plugin.ErrSkipPlugin,
		},
		{
			name:        "OTEL_SDK_DISABLED=1 with endpoint, expect ErrSkipPlugin",
			endpoint:    "http://localhost:4318",
			sdkDisabled: "1",
			output:      plugin.ErrSkipPlugin,
		},
		{
			name:        "OTEL_SDK_DISABLED=false with endpoint, expect no error",
			endpoint:    "http://localhost:4318",
			sdkDisabled: "false",
			output:      nil,
		},
		{
			name:        "OTEL_SDK_DISABLED invalid value, expect ErrInvalidArgument",
			endpoint:    "http://localhost:4318",
			sdkDisabled: "invalid",
			output:      errdefs.ErrInvalidArgument,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(otlpEndpointEnv, tc.endpoint)
			t.Setenv(otlpTracesEndpointEnv, tc.tracesEndpoint)
			t.Setenv(sdkDisabledEnv, tc.sdkDisabled)

			err := checkDisabled()
			if tc.output == nil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			} else {
				if !errors.Is(err, tc.output) {
					t.Fatalf("expected error %v, got %v", tc.output, err)
				}
			}
		})
	}
}

func TestNewTracer(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	proc := trace.NewBatchSpanProcessor(exp)
	procs := []trace.SpanProcessor{proc}

	ctx := context.TODO()
	tracerCloser, err := newTracer(ctx, procs)
	if err != nil {
		t.Fatalf("unexpected error creating tracer: %v", err)
	}
	defer tracerCloser.Close()
}
