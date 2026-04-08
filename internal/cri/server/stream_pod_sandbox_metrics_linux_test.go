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
