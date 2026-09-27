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
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/containerd/errdefs"
	"github.com/containerd/plugin"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestNewExporter(t *testing.T) {
	for _, tc := range []struct {
		name          string
		protocol      string
		traceProtocol string
		expectedErr   error
	}{
		{
			name:        "default protocol",
			expectedErr: nil,
		},
		{
			name:        "http/protobuf protocol",
			protocol:    "http/protobuf",
			expectedErr: nil,
		},
		{
			name:        "grpc protocol",
			protocol:    "grpc",
			expectedErr: nil,
		},
		{
			name:          "traces protocol overrides generic protocol",
			protocol:      "http/protobuf",
			traceProtocol: "grpc",
			expectedErr:   nil,
		},
		{
			name:        "unsupported protocol http/json",
			protocol:    "http/json",
			expectedErr: errdefs.ErrNotImplemented,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(otlpProtocolEnv, tc.protocol)
			t.Setenv(otlpTracesProtocolEnv, tc.traceProtocol)

			ctx := context.Background()
			exp, err := newExporter(ctx)
			if tc.expectedErr != nil {
				if !errors.Is(err, tc.expectedErr) {
					t.Fatalf("expected error %v, got %v", tc.expectedErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if exp == nil {
				t.Fatal("expected exporter, got nil")
			}
		})
	}
}

func TestNewTracerServiceName(t *testing.T) {
	if os.Getenv("TEST_SERVICE_NAME_HELPER") == "1" {
		expectedService := os.Getenv("EXPECTED_SERVICE_NAME")
		rec := tracetest.NewSpanRecorder()
		closer, err := newTracer(context.Background(), []trace.SpanProcessor{rec})
		if err != nil {
			t.Fatalf("unexpected error creating tracer: %v", err)
		}
		defer closer.Close()

		ctx := context.Background()
		tracer := otel.GetTracerProvider().Tracer("test-tracer")
		_, span := tracer.Start(ctx, "test-span")
		span.End()

		ended := rec.Ended()
		if len(ended) != 1 {
			t.Fatalf("expected 1 ended span, got %d", len(ended))
		}

		val, ok := ended[0].Resource().Set().Value("service.name")
		if !ok {
			t.Fatalf("expected service.name attribute on resource, but found none")
		}
		svc := val.AsString()
		if svc != expectedService {
			t.Fatalf("expected service name %q, got %q", expectedService, svc)
		}
		return
	}

	for _, tc := range []struct {
		name            string
		envServiceName  string
		expectedService string
	}{
		{
			name:            "default service name",
			envServiceName:  "",
			expectedService: "containerd",
		},
		{
			name:            "custom service name",
			envServiceName:  "custom-containerd",
			expectedService: "custom-containerd",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestNewTracerServiceName$")
			var env []string
			for _, e := range os.Environ() {
				if !strings.HasPrefix(e, "OTEL_SERVICE_NAME=") &&
					!strings.HasPrefix(e, "TEST_SERVICE_NAME_HELPER=") &&
					!strings.HasPrefix(e, "EXPECTED_SERVICE_NAME=") {
					env = append(env, e)
				}
			}
			env = append(env, "TEST_SERVICE_NAME_HELPER=1", "EXPECTED_SERVICE_NAME="+tc.expectedService)
			if tc.envServiceName != "" {
				env = append(env, "OTEL_SERVICE_NAME="+tc.envServiceName)
			}
			cmd.Env = env
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("helper process failed: %v\nOutput: %s", err, string(out))
			}
		})
	}
}

func TestNewTracerSampling(t *testing.T) {
	for _, tc := range []struct {
		name        string
		sampler     string
		samplerArg  string
		wantSampled bool
	}{
		{
			name:        "always_on sampler",
			sampler:     "always_on",
			wantSampled: true,
		},
		{
			name:        "always_off sampler",
			sampler:     "always_off",
			wantSampled: false,
		},
		{
			name:        "traceidratio 1.0",
			sampler:     "traceidratio",
			samplerArg:  "1.0",
			wantSampled: true,
		},
		{
			name:        "traceidratio 0.0",
			sampler:     "traceidratio",
			samplerArg:  "0.0",
			wantSampled: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("OTEL_TRACES_SAMPLER", tc.sampler)
			t.Setenv("OTEL_TRACES_SAMPLER_ARG", tc.samplerArg)

			rec := tracetest.NewSpanRecorder()
			closer, err := newTracer(context.Background(), []trace.SpanProcessor{rec})
			if err != nil {
				t.Fatalf("unexpected error creating tracer: %v", err)
			}
			defer closer.Close()

			ctx := context.Background()
			tracer := otel.GetTracerProvider().Tracer("test-tracer")
			_, span := tracer.Start(ctx, "test-span")
			defer span.End()

			if got := span.SpanContext().IsSampled(); got != tc.wantSampled {
				t.Fatalf("expected span sampled=%v, got %v", tc.wantSampled, got)
			}
		})
	}
}

func TestCheckDisabled(t *testing.T) {
	for _, tc := range []struct {
		name           string
		sdkDisabled    string
		endpoint       string
		tracesEndpoint string
		expectedErr    error
	}{
		{
			name:        "endpoints not configured",
			expectedErr: plugin.ErrSkipPlugin,
		},
		{
			name:        "endpoint configured",
			endpoint:    "http://localhost:4318",
			expectedErr: nil,
		},
		{
			name:           "traces endpoint configured",
			tracesEndpoint: "http://localhost:4318",
			expectedErr:    nil,
		},
		{
			name:        "sdk disabled true",
			sdkDisabled: "true",
			endpoint:    "http://localhost:4318",
			expectedErr: plugin.ErrSkipPlugin,
		},
		{
			name:        "sdk disabled 1",
			sdkDisabled: "1",
			endpoint:    "http://localhost:4318",
			expectedErr: plugin.ErrSkipPlugin,
		},
		{
			name:        "sdk disabled false",
			sdkDisabled: "false",
			endpoint:    "http://localhost:4318",
			expectedErr: nil,
		},
		{
			name:        "sdk disabled invalid",
			sdkDisabled: "invalid",
			endpoint:    "http://localhost:4318",
			expectedErr: errdefs.ErrInvalidArgument,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(sdkDisabledEnv, tc.sdkDisabled)
			t.Setenv(otlpEndpointEnv, tc.endpoint)
			t.Setenv(otlpTracesEndpointEnv, tc.tracesEndpoint)

			err := checkDisabled()
			if tc.expectedErr != nil {
				if !errors.Is(err, tc.expectedErr) {
					t.Fatalf("expected error %v, got %v", tc.expectedErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
