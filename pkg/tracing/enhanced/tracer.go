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

package enhanced

import (
	"context"
	"fmt"
	"time"

	"github.com/containerd/containerd/v2/pkg/tracing/exporters"
)

// Tracer interface for enhanced tracing
type Tracer interface {
	StartSpan(ctx context.Context, operationName string, traceID string, attrs ...Attribute) (Span, context.Context)
	IsEnabled() bool
	Shutdown(ctx context.Context) error
}

// EnhancedTracer implements the enhanced tracing functionality
type EnhancedTracer struct {
	exporters []exporters.Exporter
	config    Config
}

type Config struct {
	SamplingRate     float64
	UseSandboxID     bool
	MaxSpansPerTrace int
}

func NewEnhancedTracer(exporters []exporters.Exporter, config Config) *EnhancedTracer {
	return &EnhancedTracer{
		exporters: exporters,
		config:    config,
	}
}

func (t *EnhancedTracer) StartSpan(ctx context.Context, operationName string, traceID string, attrs ...Attribute) (Span, context.Context) {
	enhancedSpan := &EnhancedSpan{
		tracer:     t,
		startTime:  time.Now(),
		attributes: make(map[string]interface{}),
		name:       operationName,
		traceID:    traceID,
		spanID:     generateSpanID(),
	}

	// Apply attributes
	for _, attr := range attrs {
		enhancedSpan.attributes[attr.Key] = attr.Value
	}

	// Apply sandbox ID as trace ID if configured and available
	if t.config.UseSandboxID {
		if sandboxID := extractSandboxID(ctx); sandboxID != "" {
			enhancedSpan.traceID = sandboxID
			enhancedSpan.attributes["sandbox.id"] = sandboxID
		}
	}

	newCtx := ContextWithSpan(ctx, enhancedSpan)
	return enhancedSpan, newCtx
}

func (t *EnhancedTracer) ExportSpan(spanData exporters.SpanData) {
	for _, exporter := range t.exporters {
		go exporter.ExportSpan(context.Background(), spanData)
	}
}

func (t *EnhancedTracer) IsEnabled() bool {
	return len(t.exporters) > 0
}

func (t *EnhancedTracer) Shutdown(ctx context.Context) error {
	var errs []error
	for _, exporter := range t.exporters {
		if err := exporter.Shutdown(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("multiple errors during shutdown: %v", errs)
	}
	return nil
}

// Helper function to extract sandbox ID from context
func extractSandboxID(ctx context.Context) string {
	if value := ctx.Value("sandbox.id"); value != nil {
		if sandboxID, ok := value.(string); ok {
			return sandboxID
		}
	}
	return ""
}

func generateSpanID() string {
	// Simple implementation - use timestamp-based ID
	return fmt.Sprintf("%x", time.Now().UnixNano())
}
