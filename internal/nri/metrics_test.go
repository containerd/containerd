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

package nri

import (
	"context"
	"fmt"
	"maps"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestNriMetricsRecordPluginInvocation(t *testing.T) {
	m := newNRIMetrics()

	tests := []struct {
		name       string
		err        error
		wantLabels map[string]string
	}{
		{
			name:       "success",
			err:        nil,
			wantLabels: map[string]string{"status": "success", "error": ""},
		},
		{
			name:       "generic_error",
			err:        fmt.Errorf("test error"),
			wantLabels: map[string]string{"status": "error", "error": "other"},
		},
		{
			name:       "deadline_exceeded_error",
			err:        context.DeadlineExceeded,
			wantLabels: map[string]string{"status": "error", "error": "deadline_exceeded"},
		},
		{
			name:       "canceled_error",
			err:        context.Canceled,
			wantLabels: map[string]string{"status": "error", "error": "canceled"},
		},
		{
			name:       "grpc_error_invalid_argument",
			err:        status.Error(codes.InvalidArgument, "invalid argument"),
			wantLabels: map[string]string{"status": "error", "error": "invalid_argument"},
		},
		{
			name:       "grpc_error_not_found",
			err:        status.Error(codes.NotFound, "not found"),
			wantLabels: map[string]string{"status": "error", "error": "not_found"},
		},
		{
			name:       "grpc_error_deadline_exceeded",
			err:        status.Error(codes.DeadlineExceeded, "deadline exceeded"),
			wantLabels: map[string]string{"status": "error", "error": "deadline_exceeded"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m.RecordPluginInvocation(tt.name, "Synchronize", tt.err)

			labels := map[string]string{
				"plugin":    tt.name,
				"operation": "Synchronize",
			}
			maps.Copy(labels, tt.wantLabels)

			assertCounter(t, "containerd_nri_plugin_invocations_total", labels, 1.0)
		})
	}
}

func TestNriMetricsRecordPluginLatency(t *testing.T) {
	m := newNRIMetrics()

	m.RecordPluginLatency("test-plugin-2", "Synchronize", 500*time.Millisecond)

	assertTimer(t, "containerd_nri_plugin_latency_seconds", map[string]string{
		"plugin":    "test-plugin-2",
		"operation": "Synchronize",
	}, 0.5)
}

func TestNriMetricsRecordPluginAdjustments(t *testing.T) {
	m := newNRIMetrics()

	// passing nil adjustment is supported by the RecordPluginAdjustments signature
	m.RecordPluginAdjustments("test-plugin-3", "CreateContainer", nil, 2, 1)

	assertCounter(t, "containerd_nri_plugin_adjustments_total", map[string]string{
		"plugin":    "test-plugin-3",
		"operation": "CreateContainer",
		"type":      "update",
	}, 2.0)

	assertCounter(t, "containerd_nri_plugin_adjustments_total", map[string]string{
		"plugin":    "test-plugin-3",
		"operation": "CreateContainer",
		"type":      "evict",
	}, 1.0)
}

func TestNriMetricsUpdatePluginCount(t *testing.T) {
	m := newNRIMetrics()

	m.UpdatePluginCount(5)

	assertGauge(t, "containerd_nri_active_plugins_total", 5.0)
}

func matchesLabels(labels []*dto.LabelPair, expected map[string]string) bool {
	actual := make(map[string]string)
	for _, l := range labels {
		actual[l.GetName()] = l.GetValue()
	}
	for k, v := range expected {
		if val, ok := actual[k]; !ok || val != v {
			return false
		}
	}
	return true
}

func assertCounter(t *testing.T, name string, labels map[string]string, wantMin float64) {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)

	var found bool
	for _, f := range families {
		if f.GetName() != name {
			continue
		}
		for _, m := range f.Metric {
			if matchesLabels(m.Label, labels) {
				found = true
				if m.GetCounter() != nil {
					assert.GreaterOrEqual(t, m.GetCounter().GetValue(), wantMin)
				} else {
					t.Fatalf("metric %s is not a counter", name)
				}
			}
		}
	}
	assert.True(t, found, "metric %s with labels %v not found", name, labels)
}

func assertGauge(t *testing.T, name string, want float64) {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)

	var found bool
	for _, f := range families {
		if f.GetName() != name {
			continue
		}
		for _, m := range f.Metric {
			found = true
			if m.GetGauge() != nil {
				assert.Equal(t, want, m.GetGauge().GetValue())
			} else {
				t.Fatalf("metric %s is not a gauge", name)
			}
		}
	}
	assert.True(t, found, "metric %s not found", name)
}

func assertTimer(t *testing.T, name string, labels map[string]string, wantMinSum float64) {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)

	var found bool
	for _, f := range families {
		if f.GetName() != name {
			continue
		}
		for _, m := range f.Metric {
			if matchesLabels(m.Label, labels) {
				found = true
				if m.GetHistogram() != nil {
					assert.GreaterOrEqual(t, m.GetHistogram().GetSampleSum(), wantMinSum)
					assert.GreaterOrEqual(t, m.GetHistogram().GetSampleCount(), uint64(1))
				} else {
					t.Fatalf("metric %s with labels %v is not a histogram", name, labels)
				}
			}
		}
	}
	assert.True(t, found, "metric %s with labels %v not found", name, labels)
}
