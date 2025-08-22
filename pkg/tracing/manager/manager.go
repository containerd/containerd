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

package manager

import (
	"context"
	"sync"

	"github.com/containerd/containerd/v2/pkg/tracing/enhanced"
	"github.com/containerd/containerd/v2/pkg/tracing/exporters"
)

type TraceManager struct {
	config    Config
	exporters []exporters.Exporter
	mu        sync.RWMutex
	enabled   bool
	tracer    *enhanced.EnhancedTracer
}

type Config struct {
	Enabled          bool
	SamplingRate     float64
	UseSandboxID     bool
	MaxSpansPerTrace int
	Exporters        []ExporterConfig
}

type ExporterConfig struct {
	Type     string
	Endpoint string
	Options  map[string]interface{}
}

// NewTraceManager constructs a manager, instantiates exporters via factory,
// and initializes an enhanced tracer when enabled and exporters are present.
func NewTraceManager(config Config) (*TraceManager, error) {
	m := &TraceManager{
		config: config,
	}

	// Build exporters from config using factory
	var exps []exporters.Exporter
	for _, ec := range config.Exporters {
		exp, err := exporters.CreateExporter(ec.Type, ec.Endpoint, ec.Options)
		if err != nil {
			return nil, err
		}
		exps = append(exps, exp)
	}
	m.exporters = exps

	// Determine enablement
	m.enabled = config.Enabled && len(exps) > 0
	if m.enabled {
		m.tracer = enhanced.NewEnhancedTracer(exps, enhanced.Config{
			SamplingRate:     config.SamplingRate,
			UseSandboxID:     config.UseSandboxID,
			MaxSpansPerTrace: config.MaxSpansPerTrace,
		})
	}
	return m, nil
}

func (m *TraceManager) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Shutdown tracer first
	if m.tracer != nil {
		_ = m.tracer.Shutdown(ctx)
	}

	for _, exporter := range m.exporters {
		_ = exporter.Shutdown(ctx)
	}
	m.enabled = false
	return nil
}

func (m *TraceManager) IsEnabled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.enabled
}

// GetTracer returns the enhanced tracer instance if available.
func (m *TraceManager) GetTracer() enhanced.Tracer {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.tracer
}
