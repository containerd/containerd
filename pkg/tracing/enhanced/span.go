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
	"sync"
	"time"

	"github.com/containerd/containerd/v2/pkg/tracing/exporters"
)

// Span interface for enhanced tracing spans
type Span interface {
	SetAttribute(key string, value interface{})
	SetAttributes(attrs ...Attribute)
	SetStatus(code StatusCode, message string)
	RecordError(err error)
	AddEvent(name string, attrs ...Attribute)
	End()
	IsRecording() bool
}

// EnhancedSpan implements the enhanced span functionality
type EnhancedSpan struct {
	tracer     *EnhancedTracer
	startTime  time.Time
	endTime    time.Time
	attributes map[string]interface{}
	name       string
	traceID    string
	spanID     string
	status     StatusCode
	statusMsg  string
	mu         sync.RWMutex
}

// Attribute represents a key-value pair for span attributes
type Attribute struct {
	Key   string
	Value interface{}
}

// StatusCode represents span status codes
type StatusCode int

const (
	StatusCodeUnset StatusCode = 0
	StatusCodeOk    StatusCode = 1
	StatusCodeError StatusCode = 2
)

func (s *EnhancedSpan) SetAttribute(key string, value interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.attributes == nil {
		s.attributes = make(map[string]interface{})
	}
	s.attributes[key] = value
}

func (s *EnhancedSpan) SetAttributes(attrs ...Attribute) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.attributes == nil {
		s.attributes = make(map[string]interface{})
	}
	for _, attr := range attrs {
		s.attributes[attr.Key] = attr.Value
	}
}

func (s *EnhancedSpan) SetStatus(code StatusCode, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = code
	s.statusMsg = message
}

func (s *EnhancedSpan) RecordError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = StatusCodeError
	if err != nil {
		s.statusMsg = err.Error()
		s.attributes["error"] = err.Error()
	}
}

func (s *EnhancedSpan) AddEvent(name string, attrs ...Attribute) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.attributes == nil {
		s.attributes = make(map[string]interface{})
	}
	for _, attr := range attrs {
		s.attributes["event."+name+"."+attr.Key] = attr.Value
	}
}

func (s *EnhancedSpan) End() {
	s.mu.Lock()
	s.endTime = time.Now()
	spanData := exporters.SpanData{
		TraceID:    s.traceID,
		SpanID:     s.spanID,
		Name:       s.name,
		StartTime:  s.startTime,
		EndTime:    s.endTime,
		Duration:   s.endTime.Sub(s.startTime),
		Attributes: s.attributes,
		Status:     s.statusMsg,
	}
	s.mu.Unlock()

	s.tracer.ExportSpan(spanData)
}

func (s *EnhancedSpan) IsRecording() bool {
	// A span is recording until it is ended.
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.endTime.IsZero()
}
