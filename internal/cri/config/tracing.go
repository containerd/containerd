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

package config

import (
	"os"
	"strconv"
	"strings"
)

// LifecycleTracingConfig holds tracing settings used by CRI to initialize TraceManager.
type LifecycleTracingConfig struct {
	Enabled          bool             `toml:"enabled" json:"enabled"`
	SamplingRate     float64          `toml:"sampling_rate" json:"sampling_rate"`
	UseSandboxID     bool             `toml:"use_sandbox_id" json:"use_sandbox_id"`
	MaxSpansPerTrace int              `toml:"max_spans_per_trace" json:"max_spans_per_trace"`
	Exporters        []ExporterConfig `toml:"exporters" json:"exporters"`
}

// ExporterConfig describes one tracing exporter endpoint and options.
type ExporterConfig struct {
	Type     string                 `toml:"type" json:"type"`
	Endpoint string                 `toml:"endpoint" json:"endpoint"`
	Options  map[string]interface{} `toml:"options" json:"options"`
}

const (
	// Env var prefix and keys (aligned with tracing plugin for consistency)
	envTracingPrefix       = "CONTAINERD_TRACING_"
	envTracingEnabled      = envTracingPrefix + "ENABLED"
	envTracingSamplingRate = envTracingPrefix + "SAMPLING_RATE"
	envTracingUseSandboxID = envTracingPrefix + "USE_SANDBOX_ID"
	envTracingMaxSpans     = envTracingPrefix + "MAX_SPANS_PER_TRACE"
	envTracingExporters    = envTracingPrefix + "EXPORTERS"
)

// ApplyLifecycleTracingFromEnv populates c.LifecycleTracing from environment variables
func (config *Config) ApplyLifecycleTracingFromEnv() {
	if config.LifecycleTracing != nil {
		return
	}
	var anySet bool
	cfg := &LifecycleTracingConfig{
		// Provide safe defaults; they will only matter when Enabled=true.
		SamplingRate:     0.1,
		UseSandboxID:     true,
		MaxSpansPerTrace: 1000,
	}

	if v := os.Getenv(envTracingEnabled); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.Enabled = b
			anySet = true
		}
	}
	if v := os.Getenv(envTracingSamplingRate); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.SamplingRate = f
			anySet = true
		}
	}
	if v := os.Getenv(envTracingUseSandboxID); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.UseSandboxID = b
			anySet = true
		}
	}
	if v := os.Getenv(envTracingMaxSpans); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			cfg.MaxSpansPerTrace = i
			anySet = true
		}
	}
	if v := os.Getenv(envTracingExporters); v != "" {
		cfg.Exporters = parseExportersFromEnv(v)
		anySet = true
	}

	if anySet {
		config.LifecycleTracing = cfg
	}
}

// parseExportersFromEnv parses exporter list from env string.
// Format example:
//
//	"file:/var/log/containerd/traces/traces.json,format=json;zipkin:http://127.0.0.1:9411/api/v2/spans"
func parseExportersFromEnv(envVal string) []ExporterConfig {
	var out []ExporterConfig
	entries := strings.Split(envVal, ";")
	for _, e := range entries {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		parts := strings.SplitN(e, ":", 2)
		if len(parts) < 2 {
			continue
		}
		typ := strings.TrimSpace(parts[0])
		rest := strings.TrimSpace(parts[1])

		endAndOpts := strings.SplitN(rest, ",", 2)
		endpoint := strings.TrimSpace(endAndOpts[0])

		opts := map[string]interface{}{}
		if len(endAndOpts) == 2 {
			for _, kv := range strings.Split(endAndOpts[1], ",") {
				kv = strings.TrimSpace(kv)
				if kv == "" {
					continue
				}
				pair := strings.SplitN(kv, "=", 2)
				if len(pair) == 2 {
					opts[strings.TrimSpace(pair[0])] = strings.TrimSpace(pair[1])
				}
			}
		}

		out = append(out, ExporterConfig{
			Type:     typ,
			Endpoint: endpoint,
			Options:  opts,
		})
	}
	return out
}
