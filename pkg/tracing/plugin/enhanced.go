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
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/containerd/containerd/v2/pkg/tracing"
	"github.com/containerd/containerd/v2/pkg/tracing/manager"
	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"
)

const (
	enhancedTracingPlugin = "enhanced"
	envPrefix             = "CONTAINERD_TRACING_"

	// Enhanced tracing specific environment variables
	envEnabled          = envPrefix + "ENABLED"
	envSamplingRate     = envPrefix + "SAMPLING_RATE"
	envUseSandboxID     = envPrefix + "USE_SANDBOX_ID"
	envMaxSpansPerTrace = envPrefix + "MAX_SPANS_PER_TRACE"
	envExporters        = envPrefix + "EXPORTERS"
)

func init() {
	registry.Register(&plugin.Registration{
		ID:     enhancedTracingPlugin,
		Type:   plugins.TracingProcessorPlugin,
		Config: &EnhancedConfig{},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			if err := checkEnhancedDisabled(); err != nil {
				return nil, err
			}

			config, err := loadEnhancedConfig(ic)
			if err != nil {
				return nil, err
			}

			// Create trace manager
			traceManager, err := manager.NewTraceManager(config)
			if err != nil {
				return nil, fmt.Errorf("failed to create enhanced trace manager: %w", err)
			}

			// Set global trace manager for enhanced tracing
			tracing.SetGlobalTraceManager(traceManager)

			return traceManager, nil
		},
	})

	// Register enhanced tracing as an internal plugin that can be used alongside OTEL
	registry.Register(&plugin.Registration{
		ID:     "enhanced_tracing",
		Type:   plugins.InternalPlugin,
		Config: &EnhancedConfig{},
		Requires: []plugin.Type{
			plugins.TracingProcessorPlugin,
		},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			// Get the tracing processor plugins
			tracingProcessors, err := ic.GetByType(plugins.TracingProcessorPlugin)
			if err != nil {
				return nil, fmt.Errorf("failed to get tracing processors: %w", err)
			}

			// Try to find the enhanced trace manager
			for _, p := range tracingProcessors {
				if tm, ok := p.(*manager.TraceManager); ok {
					return tm, nil
				}
			}

			// fallback: return noop manager to avoid breaking integration tests
			fmt.Fprintf(os.Stderr, "enhanced tracing manager not found, falling back to noop manager\n")
			return manager.NewNoopManager(), nil
		},
	})
}

// EnhancedConfig holds the configurations for enhanced tracing
type EnhancedConfig struct {
	Enabled          bool                     `toml:"enabled,omitempty"`
	SamplingRate     float64                  `toml:"sampling_rate,omitempty"`
	UseSandboxID     bool                     `toml:"use_sandbox_id,omitempty"`
	MaxSpansPerTrace int                      `toml:"max_spans_per_trace,omitempty"`
	Exporters        []EnhancedExporterConfig `toml:"exporters,omitempty"`
}

// EnhancedExporterConfig holds configuration for enhanced tracing exporters
type EnhancedExporterConfig struct {
	Type     string                 `toml:"type,omitempty"`
	Endpoint string                 `toml:"endpoint,omitempty"`
	Options  map[string]interface{} `toml:"options,omitempty"`
}

// checkEnhancedDisabled checks if enhanced tracing is disabled via environment variables
func checkEnhancedDisabled() error {
	if v := os.Getenv(envEnabled); v != "" {
		if disabled, err := strconv.ParseBool(v); err == nil && disabled {
			return fmt.Errorf("%w: enhanced tracing disabled by env %s=%s", plugin.ErrSkipPlugin, envEnabled, v)
		}
	}

	// Check if any exporters are configured
	if os.Getenv(envExporters) == "" {
		return fmt.Errorf("%w: enhanced tracing exporters not configured", plugin.ErrSkipPlugin)
	}

	return nil
}

// loadEnhancedConfig loads enhanced tracing configuration from environment variables and plugin config
func loadEnhancedConfig(ic *plugin.InitContext) (manager.Config, error) {
	config := manager.Config{
		Enabled:          getEnvBool(envEnabled, false),
		SamplingRate:     getEnvFloat(envSamplingRate, 0.1),
		UseSandboxID:     getEnvBool(envUseSandboxID, true),
		MaxSpansPerTrace: getEnvInt(envMaxSpansPerTrace, 1000),
	}

	// Load exporters from environment
	if envVal := os.Getenv(envExporters); envVal != "" {
		config.Exporters = parseExportersFromEnv(envVal)
	}

	// Override with plugin config if provided
	if ic.Config != nil {
		enhancedConfig := ic.Config.(*EnhancedConfig)
		config = mergeConfigs(config, enhancedConfig)
	}

	return config, nil
}

// mergeConfigs merges environment-based config with plugin config
func mergeConfigs(envConfig manager.Config, pluginConfig *EnhancedConfig) manager.Config {
	config := envConfig

	// Only override if plugin config has explicit values
	if pluginConfig.Enabled {
		config.Enabled = pluginConfig.Enabled
	}
	if pluginConfig.SamplingRate > 0 {
		config.SamplingRate = pluginConfig.SamplingRate
	}
	if pluginConfig.UseSandboxID {
		config.UseSandboxID = pluginConfig.UseSandboxID
	}
	if pluginConfig.MaxSpansPerTrace > 0 {
		config.MaxSpansPerTrace = pluginConfig.MaxSpansPerTrace
	}
	if len(pluginConfig.Exporters) > 0 {
		config.Exporters = convertExporterConfigs(pluginConfig.Exporters)
	}

	return config
}

// Helper functions for environment variable parsing
func getEnvBool(key string, defaultValue bool) bool {
	if envVal := os.Getenv(key); envVal != "" {
		if val, err := strconv.ParseBool(envVal); err == nil {
			return val
		}
	}
	return defaultValue
}

func getEnvFloat(key string, defaultValue float64) float64 {
	if envVal := os.Getenv(key); envVal != "" {
		if val, err := strconv.ParseFloat(envVal, 64); err == nil {
			return val
		}
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if envVal := os.Getenv(key); envVal != "" {
		if val, err := strconv.Atoi(envVal); err == nil {
			return val
		}
	}
	return defaultValue
}

// parseExportersFromEnv parses exporter configuration from environment variable
func parseExportersFromEnv(envValue string) []manager.ExporterConfig {
	var exporterConfigs []manager.ExporterConfig

	// Split by semicolon to get individual exporter configs
	exporterStrings := strings.Split(envValue, ";")
	for _, expStr := range exporterStrings {
		if expStr == "" {
			continue
		}

		// Split by colon to get type and rest
		parts := strings.SplitN(expStr, ":", 2)
		if len(parts) < 2 {
			continue
		}

		exporterType := strings.TrimSpace(parts[0])
		rest := strings.TrimSpace(parts[1])

		// Split rest by comma to get endpoint and options
		endpointParts := strings.SplitN(rest, ",", 2)
		endpoint := strings.TrimSpace(endpointParts[0])

		options := make(map[string]interface{})
		if len(endpointParts) > 1 {
			// Parse options (format: "key1=value1,key2=value2")
			optionPairs := strings.Split(endpointParts[1], ",")
			for _, pair := range optionPairs {
				kv := strings.SplitN(pair, "=", 2)
				if len(kv) == 2 {
					options[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
				}
			}
		}

		exporterConfigs = append(exporterConfigs, manager.ExporterConfig{
			Type:     exporterType,
			Endpoint: endpoint,
			Options:  options,
		})
	}

	return exporterConfigs
}

// convertExporterConfigs converts plugin exporter configs to manager configs
func convertExporterConfigs(pluginExporters []EnhancedExporterConfig) []manager.ExporterConfig {
	var exporterConfigs []manager.ExporterConfig
	for _, exp := range pluginExporters {
		exporterConfigs = append(exporterConfigs, manager.ExporterConfig{
			Type:     exp.Type,
			Endpoint: exp.Endpoint,
			Options:  exp.Options,
		})
	}
	return exporterConfigs
}
