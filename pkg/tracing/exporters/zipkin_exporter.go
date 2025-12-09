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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

type ZipkinExporter struct {
	endpoint string
	client   *http.Client
	batch    []ZipkinSpan
	mu       sync.Mutex
}

func NewZipkinExporter(endpoint string, options map[string]interface{}) (*ZipkinExporter, error) {
	return &ZipkinExporter{
		endpoint: endpoint,
		client:   &http.Client{Timeout: 10 * time.Second},
		batch:    make([]ZipkinSpan, 0),
	}, nil
}

func (z *ZipkinExporter) ExportSpan(ctx context.Context, spanData SpanData) error {
	z.mu.Lock()
	defer z.mu.Unlock()

	zipkinSpan := z.convertToZipkin(spanData)
	z.batch = append(z.batch, zipkinSpan)

	// Simple batching threshold
	if len(z.batch) >= 100 {
		return z.flushBatch()
	}
	return nil
}

func (z *ZipkinExporter) convertToZipkin(spanData SpanData) ZipkinSpan {
	tags := make(map[string]string)
	for k, v := range spanData.Attributes {
		tags[k] = fmt.Sprintf("%v", v)
	}
	return ZipkinSpan{
		TraceID:   spanData.TraceID,
		ID:        spanData.SpanID,
		Name:      spanData.Name,
		Timestamp: spanData.StartTime.UnixNano() / 1000,
		Duration:  int64(spanData.Duration / time.Microsecond),
		Tags:      tags,
		LocalEndpoint: &ZipkinEndpoint{
			ServiceName: "containerd",
		},
	}
}

func (z *ZipkinExporter) flushBatch() error {
	if len(z.batch) == 0 {
		return nil
	}
	jsonData, err := json.Marshal(z.batch)
	if err != nil {
		return err
	}
	resp, err := z.client.Post(z.endpoint, "application/json", bytes.NewReader(jsonData))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("zipkin export failed: %s", resp.Status)
	}
	z.batch = z.batch[:0]
	return nil
}

func (z *ZipkinExporter) Shutdown(ctx context.Context) error {
	z.mu.Lock()
	defer z.mu.Unlock()
	return z.flushBatch()
}
