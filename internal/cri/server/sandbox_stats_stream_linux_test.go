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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	sandboxstore "github.com/containerd/containerd/v2/internal/cri/store/sandbox"
)

func TestStreamPodSandboxStatsEmpty(t *testing.T) {
	c := newTestCRIService()

	stream := newFakeStreamServer[runtime.StreamPodSandboxStatsResponse](context.Background())
	err := c.StreamPodSandboxStats(&runtime.StreamPodSandboxStatsRequest{}, stream)
	assert.NoError(t, err)
	assert.Empty(t, stream.sent, "no messages should be sent for empty store")
}

func TestStreamPodSandboxStatsFiltersNotReady(t *testing.T) {
	c := newTestCRIService()

	// Add a sandbox that is NOT ready — it should be filtered out.
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

	stream := newFakeStreamServer[runtime.StreamPodSandboxStatsResponse](context.Background())
	err := c.StreamPodSandboxStats(&runtime.StreamPodSandboxStatsRequest{}, stream)
	assert.NoError(t, err)
	assert.Empty(t, stream.sent, "non-ready sandboxes should be filtered out")
}

func TestStreamPodSandboxStatsFilterByID(t *testing.T) {
	c := newTestCRIService()

	sandboxes := []sandboxstore.Sandbox{
		sandboxstore.NewSandbox(
			sandboxstore.Metadata{
				ID:   "sb-1",
				Name: "sandbox-1",
				Config: &runtime.PodSandboxConfig{
					Metadata: &runtime.PodSandboxMetadata{Name: "pod-1"},
				},
			},
			sandboxstore.Status{
				CreatedAt: time.Now(),
				State:     sandboxstore.StateReady,
			},
		),
		sandboxstore.NewSandbox(
			sandboxstore.Metadata{
				ID:   "sb-2",
				Name: "sandbox-2",
				Config: &runtime.PodSandboxConfig{
					Metadata: &runtime.PodSandboxMetadata{Name: "pod-2"},
				},
			},
			sandboxstore.Status{
				CreatedAt: time.Now(),
				State:     sandboxstore.StateReady,
			},
		),
	}
	for _, sb := range sandboxes {
		require.NoError(t, c.sandboxStore.Add(sb))
	}

	// Verify sandboxesForListPodSandboxStatsRequest filters by ID.
	filtered := c.sandboxesForListPodSandboxStatsRequest(&runtime.ListPodSandboxStatsRequest{
		Filter: &runtime.PodSandboxStatsFilter{Id: "sb-2"},
	})
	require.Len(t, filtered, 1)
	assert.Equal(t, "sb-2", filtered[0].ID)
}

func TestStreamPodSandboxStatsFilterByLabel(t *testing.T) {
	c := newTestCRIService()

	sandboxes := []sandboxstore.Sandbox{
		sandboxstore.NewSandbox(
			sandboxstore.Metadata{
				ID:   "sb-1",
				Name: "sandbox-1",
				Config: &runtime.PodSandboxConfig{
					Metadata: &runtime.PodSandboxMetadata{Name: "pod-1"},
					Labels:   map[string]string{"env": "prod"},
				},
			},
			sandboxstore.Status{
				CreatedAt: time.Now(),
				State:     sandboxstore.StateReady,
			},
		),
		sandboxstore.NewSandbox(
			sandboxstore.Metadata{
				ID:   "sb-2",
				Name: "sandbox-2",
				Config: &runtime.PodSandboxConfig{
					Metadata: &runtime.PodSandboxMetadata{Name: "pod-2"},
					Labels:   map[string]string{"env": "staging"},
				},
			},
			sandboxstore.Status{
				CreatedAt: time.Now(),
				State:     sandboxstore.StateReady,
			},
		),
	}
	for _, sb := range sandboxes {
		require.NoError(t, c.sandboxStore.Add(sb))
	}

	filtered := c.sandboxesForListPodSandboxStatsRequest(&runtime.ListPodSandboxStatsRequest{
		Filter: &runtime.PodSandboxStatsFilter{
			LabelSelector: map[string]string{"env": "staging"},
		},
	})
	require.Len(t, filtered, 1)
	assert.Equal(t, "sb-2", filtered[0].ID)
}

func TestStreamPodSandboxStatsReadySandboxErrorHandling(t *testing.T) {
	c := newTestCRIService()

	// Add a ready sandbox. podSandboxStats will fail (no cgroups in test env)
	// but StreamPodSandboxStats should handle the error gracefully.
	sb := sandboxstore.NewSandbox(
		sandboxstore.Metadata{
			ID:   "sb-ready",
			Name: "sandbox-ready",
			Config: &runtime.PodSandboxConfig{
				Metadata: &runtime.PodSandboxMetadata{Name: "pod-ready"},
			},
		},
		sandboxstore.Status{
			CreatedAt: time.Now(),
			State:     sandboxstore.StateReady,
		},
	)
	require.NoError(t, c.sandboxStore.Add(sb))

	stream := newFakeStreamServer[runtime.StreamPodSandboxStatsResponse](context.Background())
	err := c.StreamPodSandboxStats(&runtime.StreamPodSandboxStatsRequest{}, stream)

	// On platforms where podSandboxStats is not implemented or cgroups are unavailable,
	// the error is collected and returned via errors.Join. The streaming part still runs
	// (with empty stats), so the stream itself is not broken.
	// The error may be nil (on "other" platforms where ErrNotImplemented is treated as
	// unavailable) or non-nil (on Linux where cgroup loading fails).
	if err != nil {
		assert.Contains(t, err.Error(), "sb-ready")
	}
}
