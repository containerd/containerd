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
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"
)

type FileExporter struct {
	file          *os.File
	encoder       *json.Encoder
	mu            sync.Mutex
	filePath      string
	format        string // "json" or "zipkin"
	serviceName   string
	maxFileSize   int64
	currentSize   int64
	rotationCount int
}

// ZipkinSpan represents the Zipkin V2 JSON format
type ZipkinSpan struct {
	TraceID        string             `json:"traceId"`
	ID             string             `json:"id"`
	ParentID       string             `json:"parentId,omitempty"`
	Name           string             `json:"name"`
	Timestamp      int64              `json:"timestamp"` // microseconds since epoch
	Duration       int64              `json:"duration"`  // microseconds
	Kind           string             `json:"kind,omitempty"`
	LocalEndpoint  *ZipkinEndpoint    `json:"localEndpoint,omitempty"`
	RemoteEndpoint *ZipkinEndpoint    `json:"remoteEndpoint,omitempty"`
	Annotations    []ZipkinAnnotation `json:"annotations,omitempty"`
	Tags           map[string]string  `json:"tags,omitempty"`
	Debug          bool               `json:"debug,omitempty"`
	Shared         bool               `json:"shared,omitempty"`
}

type ZipkinEndpoint struct {
	ServiceName string `json:"serviceName"`
	IPv4        string `json:"ipv4,omitempty"`
	IPv6        string `json:"ipv6,omitempty"`
	Port        int    `json:"port,omitempty"`
}

type ZipkinAnnotation struct {
	Timestamp int64  `json:"timestamp"`
	Value     string `json:"value"`
}

func NewFileExporter(filePath string, options map[string]interface{}) (*FileExporter, error) {
	// Ensure directory exists
	dir := "/var/log/containerd/traces"
	if customDir, ok := options["directory"].(string); ok && customDir != "" {
		dir = customDir
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create trace directory: %w", err)
	}

	// Determine file format
	format := "json"
	if fmtVal, ok := options["format"].(string); ok && fmtVal != "" {
		format = fmtVal
	}

	// Get service name for Zipkin format
	serviceName := "containerd"
	if svcVal, ok := options["service_name"].(string); ok && svcVal != "" {
		serviceName = svcVal
	}

	// Get max file size for rotation (default 100MB)
	maxFileSize := int64(100 * 1024 * 1024)
	if sizeVal, ok := options["max_file_size"].(string); ok {
		if size, err := strconv.ParseInt(sizeVal, 10, 64); err == nil {
			maxFileSize = size
		}
	}

	file, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("failed to open trace file: %w", err)
	}

	// Get current file size
	fileInfo, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("failed to get file info: %w", err)
	}

	exporter := &FileExporter{
		file:        file,
		encoder:     json.NewEncoder(file),
		filePath:    filePath,
		format:      format,
		serviceName: serviceName,
		maxFileSize: maxFileSize,
		currentSize: fileInfo.Size(),
	}
	return exporter, nil
}

func (e *FileExporter) ExportSpan(ctx context.Context, spanData SpanData) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Rotate file if needed
	if e.currentSize >= e.maxFileSize {
		if err := e.rotateFile(); err != nil {
			return fmt.Errorf("failed to rotate file: %w", err)
		}
	}

	var data interface{}
	switch e.format {
	case "zipkin":
		data = e.convertToZipkinFormat(spanData)
	default:
		// For JSON lines; ensure duration is set
		if spanData.Duration == 0 {
			spanData.Duration = spanData.EndTime.Sub(spanData.StartTime)
		}
		data = spanData
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal span data: %w", err)
	}

	// Write one JSON per line
	if _, err := e.file.Write(append(jsonData, '\n')); err != nil {
		return err
	}
	e.currentSize += int64(len(jsonData) + 1)
	return nil
}

func (e *FileExporter) convertToZipkinFormat(spanData SpanData) ZipkinSpan {
	// Convert attributes to Zipkin tags
	tags := make(map[string]string)
	for key, value := range spanData.Attributes {
		tags[key] = fmt.Sprintf("%v", value)
	}

	// Add standard containerd tags
	tags["component"] = "containerd"
	tags["container.runtime"] = "containerd"

	// Determine span kind based on operation name
	kind := e.determineSpanKind(spanData.Name)

	// Convert times to microseconds
	timestamp := spanData.StartTime.UnixNano() / 1000
	duration := spanData.EndTime.Sub(spanData.StartTime).Microseconds()

	zs := ZipkinSpan{
		TraceID:   spanData.TraceID,
		ID:        spanData.SpanID,
		Name:      spanData.Name,
		Timestamp: timestamp,
		Duration:  duration,
		Kind:      kind,
		Tags:      tags,
		LocalEndpoint: &ZipkinEndpoint{
			ServiceName: e.serviceName,
		},
	}
	zs.Annotations = e.createAnnotations(spanData)
	return zs
}

func (e *FileExporter) determineSpanKind(operationName string) string {
	switch operationName {
	case "cri.create_container", "cri.start_container", "cri.stop_container",
		"cri.remove_container", "cri.create_sandbox", "cri.start_sandbox",
		"cri.stop_sandbox", "cri.remove_sandbox":
		return "SERVER"
	case "cni.setup_pod_network", "cni.teardown_pod_network":
		return "CLIENT"
	case "snapshot.prepare", "snapshot.commit", "mount.snapshot", "mount.unmount_snapshot":
		return "PRODUCER"
	default:
		return ""
	}
}

func (e *FileExporter) createAnnotations(spanData SpanData) []ZipkinAnnotation {
	return []ZipkinAnnotation{
		{Timestamp: spanData.StartTime.UnixNano() / 1000, Value: "start"},
		{Timestamp: spanData.EndTime.UnixNano() / 1000, Value: "end"},
	}
}

func (e *FileExporter) rotateFile() error {
	if err := e.file.Close(); err != nil {
		return err
	}
	timestamp := time.Now().Format("20060102-150405")
	newFilePath := fmt.Sprintf("%s.%d.%s", e.filePath, e.rotationCount, timestamp)
	e.rotationCount++

	if err := os.Rename(e.filePath, newFilePath); err != nil {
		return err
	}

	file, err := os.OpenFile(e.filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	e.file = file
	e.encoder = json.NewEncoder(file)
	e.currentSize = 0
	return nil
}

func (e *FileExporter) Shutdown(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.file != nil {
		return e.file.Close()
	}
	return nil
}
