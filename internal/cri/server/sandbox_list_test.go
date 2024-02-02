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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	sandboxstore "github.com/containerd/containerd/v2/internal/cri/store/sandbox"
)

func TestToCRISandbox(t *testing.T) {
	config := &runtime.PodSandboxConfig{
		Metadata: &runtime.PodSandboxMetadata{
			Name:      "test-name",
			Uid:       "test-uid",
			Namespace: "test-ns",
			Attempt:   1,
		},
		Labels:      map[string]string{"a": "b"},
		Annotations: map[string]string{"c": "d"},
	}
	createdAt := time.Now()
	meta := sandboxstore.Metadata{
		ID:             "test-id",
		Name:           "test-name",
		Config:         config,
		NetNSPath:      "test-netns",
		RuntimeHandler: "test-runtime-handler",
	}
	expect := &runtime.PodSandbox{
		Id:             "test-id",
		Metadata:       config.GetMetadata(),
		CreatedAt:      createdAt.UnixNano(),
		Labels:         config.GetLabels(),
		Annotations:    config.GetAnnotations(),
		RuntimeHandler: "test-runtime-handler",
	}
	for _, test := range []struct {
		desc          string
		state         sandboxstore.State
		expectedState runtime.PodSandboxState
	}{
		{
			desc:          "sandbox state ready",
			state:         sandboxstore.StateReady,
			expectedState: runtime.PodSandboxState_SANDBOX_READY,
		},
		{
			desc:          "sandbox state not ready",
			state:         sandboxstore.StateNotReady,
			expectedState: runtime.PodSandboxState_SANDBOX_NOTREADY,
		},
		{
			desc:          "sandbox state unknown",
			state:         sandboxstore.StateUnknown,
			expectedState: runtime.PodSandboxState_SANDBOX_NOTREADY,
		},
	} {
		test := test
		t.Run(test.desc, func(t *testing.T) {
			status := sandboxstore.Status{
				CreatedAt: createdAt,
				State:     test.state,
			}
			expect.State = test.expectedState
			s := toCRISandbox(meta, status)
			assert.Equal(t, expect, s, test.desc)
		})
	}
}

func TestFilterSandboxes(t *testing.T) {
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

	// Create PodSandbox
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
			desc:   "no filter",
			expect: testSandboxes,
		},
		{
			desc:   "id filter",
			filter: &runtime.PodSandboxFilter{Id: "2abcdef"},
			expect: []*runtime.PodSandbox{testSandboxes[1]},
		},
		{
			desc:   "truncid filter",
			filter: &runtime.PodSandboxFilter{Id: "2"},
			expect: []*runtime.PodSandbox{testSandboxes[1]},
		},
		{
			desc: "state filter",
			filter: &runtime.PodSandboxFilter{
				State: &runtime.PodSandboxStateValue{
					State: runtime.PodSandboxState_SANDBOX_READY,
				},
			},
			expect: []*runtime.PodSandbox{testSandboxes[0], testSandboxes[2]},
		},
		{
			desc: "label filter",
			filter: &runtime.PodSandboxFilter{
				LabelSelector: map[string]string{"a": "b"},
			},
			expect: []*runtime.PodSandbox{testSandboxes[1]},
		},
		{
			desc: "mixed filter not matched",
			filter: &runtime.PodSandboxFilter{
				Id:            "1",
				LabelSelector: map[string]string{"a": "b"},
			},
			expect: []*runtime.PodSandbox{},
		},
		{
			desc: "mixed filter matched",
			filter: &runtime.PodSandboxFilter{
				State: &runtime.PodSandboxStateValue{
					State: runtime.PodSandboxState_SANDBOX_READY,
				},
				LabelSelector: map[string]string{"c": "d"},
			},
			expect: []*runtime.PodSandbox{testSandboxes[2]},
		},
	} {
		test := test
		t.Run(test.desc, func(t *testing.T) {
			filtered := c.filterCRISandboxes(testSandboxes, test.filter)
			assert.Equal(t, test.expect, filtered, test.desc)
		})
	}
}
