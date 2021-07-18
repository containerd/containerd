// Copyright The OpenTelemetry Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package otlpconfig // import "go.opentelemetry.io/otel/exporters/otlp/internal/otlpconfig"

import (
	"crypto/tls"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

const (
	// DefaultMaxAttempts describes how many times the driver
	// should retry the sending of the payload in case of a
	// retryable error.
	DefaultMaxAttempts int = 5
	// DefaultTracesPath is a default URL path for endpoint that
	// receives spans.
	DefaultTracesPath string = "/v1/traces"
	// DefaultBackoff is a default base backoff time used in the
	// exponential backoff strategy.
	DefaultBackoff time.Duration = 300 * time.Millisecond
	// DefaultTimeout is a default max waiting time for the backend to process
	// each span batch.
	DefaultTimeout time.Duration = 10 * time.Second
)

var (
	// defaultRetrySettings is a default settings for the retry policy.
	defaultRetrySettings = RetrySettings{
		Enabled:         true,
		InitialInterval: 5 * time.Second,
		MaxInterval:     30 * time.Second,
		MaxElapsedTime:  time.Minute,
	}
)

type (
	SignalConfig struct {
		Endpoint    string
		Insecure    bool
		TLSCfg      *tls.Config
		Headers     map[string]string
		Compression Compression
		Timeout     time.Duration
		URLPath     string

		// gRPC configurations
		GRPCCredentials credentials.TransportCredentials
	}

	Config struct {
		// Signal specific configurations
		Traces SignalConfig

		// HTTP configurations
		MaxAttempts int
		Backoff     time.Duration

		// gRPC configurations
		ReconnectionPeriod time.Duration
		ServiceConfig      string
		DialOptions        []grpc.DialOption
		RetrySettings      RetrySettings
	}
)

func NewDefaultConfig() Config {
	c := Config{
		Traces: SignalConfig{
			Endpoint:    fmt.Sprintf("%s:%d", DefaultCollectorHost, DefaultCollectorPort),
			URLPath:     DefaultTracesPath,
			Compression: NoCompression,
			Timeout:     DefaultTimeout,
		},
		MaxAttempts:   DefaultMaxAttempts,
		Backoff:       DefaultBackoff,
		RetrySettings: defaultRetrySettings,
	}

	return c
}

type (
	// GenericOption applies an option to the HTTP or gRPC driver.
	GenericOption interface {
		ApplyHTTPOption(*Config)
		ApplyGRPCOption(*Config)

		// A private method to prevent users implementing the
		// interface and so future additions to it will not
		// violate compatibility.
		private()
	}

	// HTTPOption applies an option to the HTTP driver.
	HTTPOption interface {
		ApplyHTTPOption(*Config)

		// A private method to prevent users implementing the
		// interface and so future additions to it will not
		// violate compatibility.
		private()
	}

	// GRPCOption applies an option to the gRPC driver.
	GRPCOption interface {
		ApplyGRPCOption(*Config)

		// A private method to prevent users implementing the
		// interface and so future additions to it will not
		// violate compatibility.
		private()
	}
)

// genericOption is an option that applies the same logic
// for both gRPC and HTTP.
type genericOption struct {
	fn func(*Config)
}

func (g *genericOption) ApplyGRPCOption(cfg *Config) {
	g.fn(cfg)
}

func (g *genericOption) ApplyHTTPOption(cfg *Config) {
	g.fn(cfg)
}

func (genericOption) private() {}

func newGenericOption(fn func(cfg *Config)) GenericOption {
	return &genericOption{fn: fn}
}

// splitOption is an option that applies different logics
// for gRPC and HTTP.
type splitOption struct {
	httpFn func(*Config)
	grpcFn func(*Config)
}

func (g *splitOption) ApplyGRPCOption(cfg *Config) {
	g.grpcFn(cfg)
}

func (g *splitOption) ApplyHTTPOption(cfg *Config) {
	g.httpFn(cfg)
}

func (splitOption) private() {}

func newSplitOption(httpFn func(cfg *Config), grpcFn func(cfg *Config)) GenericOption {
	return &splitOption{httpFn: httpFn, grpcFn: grpcFn}
}

// httpOption is an option that is only applied to the HTTP driver.
type httpOption struct {
	fn func(*Config)
}

func (h *httpOption) ApplyHTTPOption(cfg *Config) {
	h.fn(cfg)
}

func (httpOption) private() {}

func NewHTTPOption(fn func(cfg *Config)) HTTPOption {
	return &httpOption{fn: fn}
}

// grpcOption is an option that is only applied to the gRPC driver.
type grpcOption struct {
	fn func(*Config)
}

func (h *grpcOption) ApplyGRPCOption(cfg *Config) {
	h.fn(cfg)
}

func (grpcOption) private() {}

func NewGRPCOption(fn func(cfg *Config)) GRPCOption {
	return &grpcOption{fn: fn}
}

// Generic Options

func WithEndpoint(endpoint string) GenericOption {
	return newGenericOption(func(cfg *Config) {
		cfg.Traces.Endpoint = endpoint
	})
}

func WithCompression(compression Compression) GenericOption {
	return newGenericOption(func(cfg *Config) {
		cfg.Traces.Compression = compression
	})
}

func WithURLPath(urlPath string) GenericOption {
	return newGenericOption(func(cfg *Config) {
		cfg.Traces.URLPath = urlPath
	})
}

func WithRetry(settings RetrySettings) GenericOption {
	return newGenericOption(func(cfg *Config) {
		cfg.RetrySettings = settings
	})
}

func WithTLSClientConfig(tlsCfg *tls.Config) GenericOption {
	return newSplitOption(func(cfg *Config) {
		cfg.Traces.TLSCfg = tlsCfg.Clone()
	}, func(cfg *Config) {
		cfg.Traces.GRPCCredentials = credentials.NewTLS(tlsCfg)
	})
}

func WithInsecure() GenericOption {
	return newGenericOption(func(cfg *Config) {
		cfg.Traces.Insecure = true
	})
}

func WithSecure() GenericOption {
	return newGenericOption(func(cfg *Config) {
		cfg.Traces.Insecure = false
	})
}

func WithHeaders(headers map[string]string) GenericOption {
	return newGenericOption(func(cfg *Config) {
		cfg.Traces.Headers = headers
	})
}

func WithTimeout(duration time.Duration) GenericOption {
	return newGenericOption(func(cfg *Config) {
		cfg.Traces.Timeout = duration
	})
}

func WithMaxAttempts(maxAttempts int) GenericOption {
	return newGenericOption(func(cfg *Config) {
		cfg.MaxAttempts = maxAttempts
	})
}

func WithBackoff(duration time.Duration) GenericOption {
	return newGenericOption(func(cfg *Config) {
		cfg.Backoff = duration
	})
}
