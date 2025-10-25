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
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
	"unicode"

	"github.com/containerd/containerd/v2/pkg/tracing/enhanced"
	"github.com/containerd/containerd/v2/pkg/tracing/exporters"
)

type Manager interface {
	Shutdown(ctx context.Context) error
	IsEnabled() bool
	GetTracer() enhanced.Tracer
}

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

	// Resolver provides sandbox.id from span attributes when absent.
	Resolver func(map[string]interface{}) (string, bool)
}

type ExporterConfig struct {
	Type     string
	Endpoint string
	Options  map[string]interface{}
}

// NewTraceManager constructs a manager, instantiates exporters via factory,
// and initializes an enhanced tracer when enabled and exporters are present.
// The enhanced tracer is created using pkg/tracing/plugin/enhanced.go.
func NewTraceManager(config Config) (*TraceManager, error) {
	m := &TraceManager{
		config: config,
	}

	var exps []exporters.Exporter
	for _, ec := range config.Exporters {
		exp, err := exporters.CreateExporter(ec.Type, ec.Endpoint, ec.Options)
		if err != nil {
			return nil, err
		}
		exps = append(exps, &traceIDNormalizingExporter{
			inner:    exp,
			resolver: config.Resolver,
		})
	}
	m.exporters = exps

	m.enabled = config.Enabled && len(exps) > 0
	if m.enabled {
		m.tracer = enhanced.NewEnhancedTracer(exps, enhanced.Config{
			SamplingRate:     config.SamplingRate,
			UseSandboxID:     config.UseSandboxID,
			MaxSpansPerTrace: config.MaxSpansPerTrace,
		}, config.Resolver)
	}
	return m, nil
}

func (m *TraceManager) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

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

type NoopManager struct{}

func (n *NoopManager) Start() error { return nil }

func NewNoopManager() Manager {
	return &NoopManager{}
}

func (n *NoopManager) Shutdown(ctx context.Context) error { return nil }
func (n *NoopManager) IsEnabled() bool                    { return false }
func (n *NoopManager) GetTracer() enhanced.Tracer         { return nil }

type traceIDNormalizingExporter struct {
	inner    exporters.Exporter
	resolver func(map[string]interface{}) (string, bool)
}

func (w *traceIDNormalizingExporter) ExportSpan(ctx context.Context, sd exporters.SpanData) error {
	if sd.Attributes == nil {
		sd.Attributes = make(map[string]interface{})
	}

	original := sd.TraceID
	if original != "" {
		sd.Attributes["trace.id"] = original
		sd.Attributes["trace.id.original"] = original
	}

	// Determine sandbox.id presence early for both normalization and "longest" tag
	var sandbox string
	if v, ok := sd.Attributes["sandbox.id"]; ok {
		if s, ok2 := v.(string); ok2 && s != "" {
			sandbox = s
		}
	}
	if sandbox == "" && w.resolver != nil {
		if s, ok := w.resolver(sd.Attributes); ok && s != "" {
			sandbox = s
			sd.Attributes["sandbox.id"] = s
		}
	}

	// Save the longest of the available original identifiers as a tag
	longest := original
	if len(sandbox) > len(longest) {
		longest = sandbox
	}
	if longest != "" {
		sd.Attributes["trace.id.longest"] = longest
	}

	// Source for normalization priority: original -> sandbox -> default
	source := strings.TrimSpace(original)
	if source == "" {
		source = sandbox
	}
	if source == "" {
		source = "containerd-default-trace"
	}

	// Enforce 64-bit (16 lowercase hex) trace id
	sd.TraceID = normalize64Hex(source)

	return w.inner.ExportSpan(ctx, sd)
}

func (w *traceIDNormalizingExporter) Shutdown(ctx context.Context) error {
	return w.inner.Shutdown(ctx)
}

func normalize64Hex(in string) string {
	s := strings.ToLower(strings.TrimSpace(in))
	if len(s) == 16 && isHex(s) {
		return s
	}
	sum := sha256.Sum256([]byte(in))
	return hex.EncodeToString(sum[:8])
}

func isHex(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsDigit(r) && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') {
			return false
		}
	}
	return true
}
