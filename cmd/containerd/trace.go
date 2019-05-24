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

package main

import (
	"github.com/sirupsen/logrus"
	"go.opencensus.io/trace"
)

type logrusExporter struct{}

func (le *logrusExporter) ExportSpan(s *trace.SpanData) {
	logrus.WithFields(logrus.Fields{
		"name":          s.Name,
		"startTime":     s.StartTime,
		"endTime":       s.EndTime,
		"duration":      s.EndTime.Sub(s.StartTime),
		"traceID":       s.TraceID.String(),
		"spanID":        s.SpanID.String(),
		"parentSpanID":  s.ParentSpanID.String(),
		"statusCode":    s.Code,
		"statusMessage": s.Message,
		"attributes":    s.Attributes,
	}).Debug("Span")
}

func init() {
	trace.RegisterExporter(&logrusExporter{})
	trace.ApplyConfig(trace.Config{DefaultSampler: trace.AlwaysSample()})
}
