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

	"github.com/containerd/containerd/v2/pkg/tracing"
	"github.com/containerd/containerd/v2/pkg/tracing/manager"
	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"
)

const enhancedTracingPlugin = "enhanced"

func init() {
	registry.Register(&plugin.Registration{
		ID:     enhancedTracingPlugin,
		Type:   plugins.InternalPlugin,
		Config: &EnhancedConfig{},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			config, err := loadEnhancedConfig(ic)
			if err != nil {
				return nil, err
			}

			if !config.Enabled || len(config.Exporters) == 0 {
				return nil, plugin.ErrSkipPlugin
			}

			traceManager, err := manager.NewTraceManager(config)
			if err != nil {
				return nil, fmt.Errorf("failed to create enhanced trace manager: %w", err)
			}

			tracing.SetGlobalTraceManager(traceManager)
			return traceManager, nil
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

// loadEnhancedConfig loads configuration only from plugin config (no env support)
func loadEnhancedConfig(ic *plugin.InitContext) (manager.Config, error) {
	if ic.Config == nil {
		return manager.Config{}, fmt.Errorf("enhanced tracing plugin requires configuration")
	}

	enhancedConfig := ic.Config.(*EnhancedConfig)
	return manager.Config{
		Enabled:          enhancedConfig.Enabled,
		SamplingRate:     enhancedConfig.SamplingRate,
		UseSandboxID:     enhancedConfig.UseSandboxID,
		MaxSpansPerTrace: enhancedConfig.MaxSpansPerTrace,
		Exporters:        convertExporterConfigs(enhancedConfig.Exporters),
	}, nil
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
