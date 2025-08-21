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

package exporters

import (
	"context"
	"time"
)

type Exporter interface {
	ExportSpan(ctx context.Context, spanData SpanData) error
	Shutdown(ctx context.Context) error
}

type SpanData struct {
	TraceID    string                 `json:"traceId"`
	SpanID     string                 `json:"spanId"`
	Name       string                 `json:"name"`
	StartTime  time.Time              `json:"startTime"`
	EndTime    time.Time              `json:"endTime"`
	Duration   time.Duration          `json:"duration"`
	Attributes map[string]interface{} `json:"attributes"`
	Status     string                 `json:"status,omitempty"`
}
