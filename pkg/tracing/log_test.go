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

package tracing

import (
	"context"
	"testing"

	"github.com/containerd/log"
	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/trace"
)

const expectedTraceIDStr = "0102030405060708090a0b0c0d0e0f10"

var (
	testTraceID = trace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	testSpanID  = trace.SpanID{1, 2, 3, 4, 5, 6, 7, 8}
)

func TestLogrusHookTraceID(t *testing.T) {
	tests := []struct {
		name        string
		enableOpt   bool
		withSpan    bool
		expectedTID string
	}{
		{
			name:        "TraceIDInjected",
			enableOpt:   true,
			withSpan:    true,
			expectedTID: expectedTraceIDStr,
		},
		{
			name:      "TraceIDNotInjected_OptionDisabled",
			enableOpt: false,
			withSpan:  true,
		},
		{
			name:      "TraceIDNotInjected_NoSpan",
			enableOpt: true,
			withSpan:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			if tc.withSpan {
				ctx = trace.ContextWithSpanContext(
					ctx,
					trace.NewSpanContext(trace.SpanContextConfig{
						TraceID: testTraceID,
						SpanID:  testSpanID,
					}),
				)
			}

			hook := NewLogrusHook(WithTraceIDField(tc.enableOpt))
			entry := &log.Entry{
				Context: ctx,
				Data:    make(log.Fields),
			}

			err := hook.Fire(entry)
			assert.NoError(t, err)

			traceID, ok := entry.Data["trace_id"]
			if tc.expectedTID != "" {
				assert.True(t, ok)
				assert.Equal(t, tc.expectedTID, traceID)
			} else {
				assert.False(t, ok)
			}
		})
	}
}
