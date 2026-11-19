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

func TestStreamPodSandboxes(t *testing.T) {
	c := newTestCRIService()
	sandboxes := []sandboxstore.Sandbox{
		sandboxstore.NewSandbox(
			sandboxstore.Metadata{
				ID:   "1abcdef",
				Name: "sandboxname-1",
				Config: &runtime.PodSandboxConfig{
					Metadata: &runtime.PodSandboxMetadata{
						Name:      "podname-1",
						Uid:       "uid-1",
						Namespace: "ns-1",
						Attempt:   1,
					},
				},
				RuntimeHandler: "test-runtime-handler",
			},
			sandboxstore.Status{
				CreatedAt: time.Now(),
				State:     sandboxstore.StateReady,
			},
		),
		sandboxstore.NewSandbox(
			sandboxstore.Metadata{
				ID:   "2abcdef",
				Name: "sandboxname-2",
				Config: &runtime.PodSandboxConfig{
					Metadata: &runtime.PodSandboxMetadata{
						Name:      "podname-2",
						Uid:       "uid-2",
						Namespace: "ns-2",
						Attempt:   2,
					},
					Labels: map[string]string{"a": "b"},
				},
				RuntimeHandler: "test-runtime-handler",
			},
			sandboxstore.Status{
				CreatedAt: time.Now(),
				State:     sandboxstore.StateNotReady,
			},
		),
		sandboxstore.NewSandbox(
			sandboxstore.Metadata{
				ID:   "3abcdef",
				Name: "sandboxname-3",
				Config: &runtime.PodSandboxConfig{
					Metadata: &runtime.PodSandboxMetadata{
						Name:      "podname-2",
						Uid:       "uid-2",
						Namespace: "ns-2",
						Attempt:   2,
					},
					Labels: map[string]string{"c": "d"},
				},
				RuntimeHandler: "test-runtime-handler",
			},
			sandboxstore.Status{
				CreatedAt: time.Now(),
				State:     sandboxstore.StateReady,
			},
		),
	}

	// Create expected PodSandboxes
	testSandboxes := []*runtime.PodSandbox{}
	for _, sb := range sandboxes {
		testSandboxes = append(testSandboxes, toCRISandbox(sb.Metadata, sb.Status.Get()))
	}

	// Inject test sandbox metadata
	for _, sb := range sandboxes {
		assert.NoError(t, c.sandboxStore.Add(sb))
	}

	for _, test := range []struct {
		desc   string
		filter *runtime.PodSandboxFilter
		expect []*runtime.PodSandbox
	}{
		{
			desc:   "stream no filter",
			expect: testSandboxes,
		},
		{
			desc:   "stream id filter",
			filter: &runtime.PodSandboxFilter{Id: "2abcdef"},
			expect: []*runtime.PodSandbox{testSandboxes[1]},
		},
		{
			desc:   "stream truncid filter",
			filter: &runtime.PodSandboxFilter{Id: "2"},
			expect: []*runtime.PodSandbox{testSandboxes[1]},
		},
		{
			desc: "stream state filter",
			filter: &runtime.PodSandboxFilter{
				State: &runtime.PodSandboxStateValue{
					State: runtime.PodSandboxState_SANDBOX_READY,
				},
			},
			expect: []*runtime.PodSandbox{testSandboxes[0], testSandboxes[2]},
		},
		{
			desc: "stream label filter",
			filter: &runtime.PodSandboxFilter{
				LabelSelector: map[string]string{"a": "b"},
			},
			expect: []*runtime.PodSandbox{testSandboxes[1]},
		},
		{
			desc: "stream mixed filter not matched",
			filter: &runtime.PodSandboxFilter{
				Id:            "1",
				LabelSelector: map[string]string{"a": "b"},
			},
			expect: []*runtime.PodSandbox{},
		},
		{
			desc: "stream mixed filter matched",
			filter: &runtime.PodSandboxFilter{
				State: &runtime.PodSandboxStateValue{
					State: runtime.PodSandboxState_SANDBOX_READY,
				},
				LabelSelector: map[string]string{"c": "d"},
			},
			expect: []*runtime.PodSandbox{testSandboxes[2]},
		},
	} {
		t.Run(test.desc, func(t *testing.T) {
			stream := newFakeStreamServer[runtime.StreamPodSandboxesResponse](context.Background())
			err := c.StreamPodSandboxes(&runtime.StreamPodSandboxesRequest{Filter: test.filter}, stream)
			assert.NoError(t, err)

			var sandboxes []*runtime.PodSandbox
			for _, resp := range stream.sent {
				sandboxes = append(sandboxes, resp.PodSandboxes...)
			}
			assert.Len(t, sandboxes, len(test.expect))
			for _, sb := range test.expect {
				assert.Contains(t, sandboxes, sb)
			}
		})
	}
}

func TestStreamPodSandboxesEmpty(t *testing.T) {
	c := newTestCRIService()

	stream := newFakeStreamServer[runtime.StreamPodSandboxesResponse](context.Background())
	err := c.StreamPodSandboxes(&runtime.StreamPodSandboxesRequest{}, stream)
	assert.NoError(t, err)
	assert.Empty(t, stream.sent, "no messages should be sent for empty store")
}

func TestStreamPodSandboxesBatching(t *testing.T) {
	c := newTestCRIService()

	// Add more sandboxes than the batch size to verify batching
	for i := 0; i < 3; i++ {
		sb := sandboxstore.NewSandbox(
			sandboxstore.Metadata{
				ID:   fmt.Sprintf("sb-%d", i),
				Name: fmt.Sprintf("sandbox-%d", i),
				Config: &runtime.PodSandboxConfig{
					Metadata: &runtime.PodSandboxMetadata{
						Name: fmt.Sprintf("pod-%d", i),
					},
				},
			},
			sandboxstore.Status{
				CreatedAt: time.Now(),
				State:     sandboxstore.StateReady,
			},
		)
		require.NoError(t, c.sandboxStore.Add(sb))
	}

	stream := newFakeStreamServer[runtime.StreamPodSandboxesResponse](context.Background())
	err := c.StreamPodSandboxes(&runtime.StreamPodSandboxesRequest{}, stream)
	assert.NoError(t, err)

	// With 3 sandboxes and batch size of 5000, should be 1 batch
	require.Len(t, stream.sent, 1)
	assert.Len(t, stream.sent[0].PodSandboxes, 3)
}
