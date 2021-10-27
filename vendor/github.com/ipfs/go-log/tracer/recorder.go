package loggabletracer

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"time"

	writer "github.com/ipfs/go-log/writer"
	opentrace "github.com/opentracing/opentracing-go"
)

// A SpanRecorder handles all of the `RawSpan` data generated via an
// associated `Tracer` (see `NewStandardTracer`) instance. It also names
// the containing process and provides access to a straightforward tag map.
type SpanRecorder interface {
	// Implementations must determine whether and where to store `span`.
	RecordSpan(span RawSpan)
}

type LoggableSpanRecorder struct{}

// NewLoggableRecorder creates new LoggableSpanRecorder
func NewLoggableRecorder() *LoggableSpanRecorder {
	return new(LoggableSpanRecorder)
}

// Loggable Representation of a span, treated as an event log
type LoggableSpan struct {
	TraceID      uint64         `json:"TraceID"`
	SpanID       uint64         `json:"SpanID"`
	ParentSpanID uint64         `json:"ParentSpanID"`
	Operation    string         `json:"Operation"`
	Start        time.Time      `json:"Start"`
	Duration     time.Duration  `json:"Duration"`
	Tags         opentrace.Tags `json:"Tags"`
	Logs         []SpanLog      `json:"Logs"`
}

type SpanLog struct {
	Timestamp time.Time   `json:"Timestamp"`
	Field     []SpanField `json:"Fields"`
}

type SpanField struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

// RecordSpan implements the respective method of SpanRecorder.
func (r *LoggableSpanRecorder) RecordSpan(span RawSpan) {
	// short circuit if theres nothing to write to
	if !writer.WriterGroup.Active() {
		return
	}

	sl := make([]SpanLog, len(span.Logs))
	for i := range span.Logs {
		sl[i].Timestamp = span.Logs[i].Timestamp
		sf := make([]SpanField, len(span.Logs[i].Fields))
		sl[i].Field = sf
		for j := range span.Logs[i].Fields {
			sf[j].Key = span.Logs[i].Fields[j].Key()
			sf[j].Value = fmt.Sprint(span.Logs[i].Fields[j].Value())
		}
	}

	tags := make(map[string]interface{}, len(span.Tags))
	for k, v := range span.Tags {
		switch vt := v.(type) {
		case bool, string, int, int8, int16, int32, int64, uint, uint8, uint16, uint64:
			tags[k] = v
		case []byte:
			base64.StdEncoding.EncodeToString(vt)
		default:
			tags[k] = fmt.Sprint(v)
		}
	}

	spanlog := &LoggableSpan{
		TraceID:      span.Context.TraceID,
		SpanID:       span.Context.SpanID,
		ParentSpanID: span.ParentSpanID,
		Operation:    span.Operation,
		Start:        span.Start,
		Duration:     span.Duration,
		Tags:         tags,
		Logs:         sl,
	}

	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	err := encoder.Encode(spanlog)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR FORMATTING SPAN ENTRY: %s\n", err)
		return
	}

	_, _ = writer.WriterGroup.Write(buf.Bytes())
}
