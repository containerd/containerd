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

package server

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	sandboxstore "github.com/containerd/containerd/v2/internal/cri/store/sandbox"
)

func TestPodSandboxMetricsWithoutNetwork(t *testing.T) {
	c := newTestCRIService()
	sb := sandboxstore.NewSandbox(
		sandboxstore.Metadata{
			ID:   "sb-isolated",
			Name: "sandbox-isolated",
			Config: &runtime.PodSandboxConfig{
				Metadata:   &runtime.PodSandboxMetadata{Name: "pod-isolated", Namespace: "test"},
				PodNetwork: runtime.PodNetwork_POD_NETWORK_NONE,
			},
		},
		sandboxstore.Status{State: sandboxstore.StateReady},
	)
	// A nonempty path must not trigger default-interface collection for an
	// isolated sandbox. A nonexistent path makes accidental access fail without
	// requiring privileges or creating a network namespace in this unit test.
	sb.NetNSPath = t.TempDir() + "/netns"
	require.NoError(t, c.sandboxStore.Add(sb))

	metrics, err := c.collectPodSandboxMetrics(context.Background(), sb, []string{"pod-isolated", "test"})
	require.NoError(t, err)
	require.NotNil(t, metrics)
	assert.Equal(t, sb.ID, metrics.PodSandboxId)
	assert.Empty(t, metrics.Metrics, "isolated sandbox must not report default-interface counters")

	resp, err := c.ListPodSandboxMetrics(context.Background(), &runtime.ListPodSandboxMetricsRequest{})
	require.NoError(t, err)
	require.Len(t, resp.PodMetrics, 1, "isolated sandbox metrics record must not be dropped")
	assert.Equal(t, sb.ID, resp.PodMetrics[0].PodSandboxId)
	assert.Empty(t, resp.PodMetrics[0].Metrics)

	stream := newFakeStreamServer[runtime.StreamPodSandboxMetricsResponse](context.Background())
	require.NoError(t, c.StreamPodSandboxMetrics(&runtime.StreamPodSandboxMetricsRequest{}, stream))
	require.Len(t, stream.sent, 1, "isolated sandbox metrics record must also be streamed")
}

func TestStreamPodSandboxMetricsEmpty(t *testing.T) {
	c := newTestCRIService()

	stream := newFakeStreamServer[runtime.StreamPodSandboxMetricsResponse](context.Background())
	err := c.StreamPodSandboxMetrics(&runtime.StreamPodSandboxMetricsRequest{}, stream)
	assert.NoError(t, err)
	assert.Empty(t, stream.sent, "no messages should be sent for empty store")
}

func TestStreamPodSandboxMetricsSkipsNotReady(t *testing.T) {
	c := newTestCRIService()

	// Add a not-ready sandbox — ListPodSandboxMetrics should skip it.
	sb := sandboxstore.NewSandbox(
		sandboxstore.Metadata{
			ID:   "sb-notready",
			Name: "sandbox-notready",
			Config: &runtime.PodSandboxConfig{
				Metadata: &runtime.PodSandboxMetadata{Name: "pod-notready"},
			},
		},
		sandboxstore.Status{
			CreatedAt: time.Now(),
			State:     sandboxstore.StateNotReady,
		},
	)
	require.NoError(t, c.sandboxStore.Add(sb))

	stream := newFakeStreamServer[runtime.StreamPodSandboxMetricsResponse](context.Background())
	err := c.StreamPodSandboxMetrics(&runtime.StreamPodSandboxMetricsRequest{}, stream)
	assert.NoError(t, err)
	assert.Empty(t, stream.sent, "not-ready sandboxes should be skipped")
}

func TestStreamPodSandboxMetricsContextCancelled(t *testing.T) {
	c := newTestCRIService()

	// Add ready sandboxes so the rate limiter is reached on Linux.
	for i := 0; i < 3; i++ {
		sb := sandboxstore.NewSandbox(
			sandboxstore.Metadata{
				ID:   fmt.Sprintf("sb-%d", i),
				Name: fmt.Sprintf("sandbox-%d", i),
				Config: &runtime.PodSandboxConfig{
					Metadata: &runtime.PodSandboxMetadata{Name: fmt.Sprintf("pod-%d", i)},
				},
			},
			sandboxstore.Status{
				CreatedAt: time.Now(),
				State:     sandboxstore.StateReady,
			},
		)
		require.NoError(t, c.sandboxStore.Add(sb))
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	stream := newFakeStreamServer[runtime.StreamPodSandboxMetricsResponse](ctx)
	err := c.StreamPodSandboxMetrics(&runtime.StreamPodSandboxMetricsRequest{}, stream)

	// With a cancelled context the rate limiter detects context cancellation and returns an error.
	assert.Error(t, err)
}
