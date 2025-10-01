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
	"crypto/rand"
	"errors"
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
	resolver  func(map[string]interface{}) (string, bool)
}

type Config struct {
	SamplingRate     float64
	UseSandboxID     bool
	MaxSpansPerTrace int
}

func NewEnhancedTracer(exporters []exporters.Exporter, config Config, resolver func(map[string]interface{}) (string, bool)) *EnhancedTracer {
	return &EnhancedTracer{
		exporters: exporters,
		config:    config,
		resolver:  resolver,
	}
}

func generateTraceID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err == nil {
		return fmt.Sprintf("%x", b)
	}
	return fmt.Sprintf("%x", time.Now().UnixNano())
}

// GetAttribute returns the attribute value for the given key if it exists.
func (s *EnhancedSpan) GetAttribute(key string) (interface{}, bool) {
	if s == nil {
		return nil, false
	}
	val, ok := s.attributes[key]
	return val, ok
}

func (t *EnhancedTracer) StartSpan(ctx context.Context, operationName string, traceID string, attrs ...Attribute) (Span, context.Context) {
	if traceID == "" {
		traceID = generateTraceID()
	}

	enhancedSpan := &EnhancedSpan{
		tracer:     t,
		startTime:  time.Now(),
		attributes: make(map[string]interface{}),
		name:       operationName,
		traceID:    traceID,
		spanID:     generateSpanID(),
	}

	for _, attr := range attrs {
		enhancedSpan.attributes[attr.Key] = attr.Value
	}

	newCtx := ContextWithSpan(ctx, enhancedSpan)
	return enhancedSpan, newCtx
}

func (t *EnhancedTracer) ExportSpan(spanData exporters.SpanData) {
	// Enrich missing sandbox.id; optionally override traceId with sandbox.id
	if t.config.UseSandboxID {
		var sbx string
		if v, ok := spanData.Attributes["sandbox.id"]; ok {
			if s, ok2 := v.(string); ok2 && s != "" {
				sbx = s
			}
		}
		if sbx == "" && t.resolver != nil {
			if s, ok := t.resolver(spanData.Attributes); ok && s != "" {
				sbx = s
				if spanData.Attributes == nil {
					spanData.Attributes = map[string]interface{}{}
				}
				spanData.Attributes["sandbox.id"] = sbx
			}
		}
		if sbx != "" {
			spanData.TraceID = sbx
		}
	}

	if spanData.Attributes != nil {
		copied := make(map[string]interface{}, len(spanData.Attributes))
		for k, v := range spanData.Attributes {
			copied[k] = v
		}
		spanData.Attributes = copied
	}

	for _, exporter := range t.exporters {
		sd := spanData
		go exporter.ExportSpan(context.Background(), sd)
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
		return fmt.Errorf("multiple errors during shutdown: %w", errors.Join(errs...))
	}
	return nil
}

func generateSpanID() string {
	return fmt.Sprintf("%x", time.Now().UnixNano())
}
