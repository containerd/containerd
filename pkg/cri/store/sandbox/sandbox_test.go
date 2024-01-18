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

package sandbox

import (
	"testing"
	"time"

	"github.com/containerd/containerd/v2/pkg/cri/store/label"
	"github.com/containerd/containerd/v2/pkg/cri/store/stats"
	"github.com/containerd/containerd/v2/pkg/errdefs"

	assertlib "github.com/stretchr/testify/assert"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestSandboxStore(t *testing.T) {
	sandboxes := map[string]Sandbox{
		"1": NewSandbox(
			Metadata{
				ID:   "1",
				Name: "Sandbox-1",
				Config: &runtime.PodSandboxConfig{
					Metadata: &runtime.PodSandboxMetadata{
						Name:      "TestPod-1",
						Uid:       "TestUid-1",
						Namespace: "TestNamespace-1",
						Attempt:   1,
					},
				},
				NetNSPath: "TestNetNS-1",
			},
			Status{State: StateReady},
		),
		"2abcd": NewSandbox(
			Metadata{
				ID:   "2abcd",
				Name: "Sandbox-2abcd",
				Config: &runtime.PodSandboxConfig{
					Metadata: &runtime.PodSandboxMetadata{
						Name:      "TestPod-2abcd",
						Uid:       "TestUid-2abcd",
						Namespace: "TestNamespace-2abcd",
						Attempt:   2,
					},
				},
				NetNSPath: "TestNetNS-2",
			},
			Status{State: StateNotReady},
		),
		"4a333": NewSandbox(
			Metadata{
				ID:   "4a333",
				Name: "Sandbox-4a333",
				Config: &runtime.PodSandboxConfig{
					Metadata: &runtime.PodSandboxMetadata{
						Name:      "TestPod-4a333",
						Uid:       "TestUid-4a333",
						Namespace: "TestNamespace-4a333",
						Attempt:   3,
					},
				},
				NetNSPath: "TestNetNS-3",
			},
			Status{State: StateNotReady},
		),
		"4abcd": NewSandbox(
			Metadata{
				ID:   "4abcd",
				Name: "Sandbox-4abcd",
				Config: &runtime.PodSandboxConfig{
					Metadata: &runtime.PodSandboxMetadata{
						Name:      "TestPod-4abcd",
						Uid:       "TestUid-4abcd",
						Namespace: "TestNamespace-4abcd",
						Attempt:   1,
					},
				},
				NetNSPath: "TestNetNS-4abcd",
			},
			Status{State: StateReady},
		),
	}
	unknown := NewSandbox(
		Metadata{
			ID:   "3defg",
			Name: "Sandbox-3defg",
			Config: &runtime.PodSandboxConfig{
				Metadata: &runtime.PodSandboxMetadata{
					Name:      "TestPod-3defg",
					Uid:       "TestUid-3defg",
					Namespace: "TestNamespace-3defg",
					Attempt:   1,
				},
			},
			NetNSPath: "TestNetNS-3defg",
		},
		Status{State: StateUnknown},
	)
	stats := map[string]*stats.ContainerStats{
		"1": {
			Timestamp:            time.Now(),
			UsageCoreNanoSeconds: 1,
		},
		"2abcd": {
			Timestamp:            time.Now(),
			UsageCoreNanoSeconds: 2,
		},
		"4a333": {
			Timestamp:            time.Now(),
			UsageCoreNanoSeconds: 3,
		},
		"4abcd": {
			Timestamp:            time.Now(),
			UsageCoreNanoSeconds: 4,
		},
	}
	assert := assertlib.New(t)
	s := NewStore(label.NewStore())

	t.Logf("should be able to add sandbox")
	for _, sb := range sandboxes {
		assert.NoError(s.Add(sb))
	}
	assert.NoError(s.Add(unknown))

	t.Logf("should be able to get sandbox")
	genTruncIndex := func(normalName string) string { return normalName[:(len(normalName)+1)/2] }
	for id, sb := range sandboxes {
		got, err := s.Get(genTruncIndex(id))
		assert.NoError(err)
		assert.Equal(sb, got)
	}

	t.Logf("should be able to get sandbox in unknown state with Get")
	got, err := s.Get(unknown.ID)
	assert.NoError(err)
	assert.Equal(unknown, got)

	t.Logf("should be able to list sandboxes")
	sbNum := len(sandboxes) + 1
	sbs := s.List()
	assert.Len(sbs, sbNum)

	t.Logf("should be able to update stats on container")
	for id := range sandboxes {
		err := s.UpdateContainerStats(id, stats[id])
		assert.NoError(err)
	}

	// Validate stats were updated
	sbs = s.List()
	assert.Len(sbs, sbNum)
	for _, sb := range sbs {
		assert.Equal(stats[sb.ID], sb.Stats)
	}

	for testID, v := range sandboxes {
		truncID := genTruncIndex(testID)

		t.Logf("add should return already exists error for duplicated sandbox")
		assert.Equal(errdefs.ErrAlreadyExists, s.Add(v))

		t.Logf("should be able to delete sandbox")
		s.Delete(truncID)
		sbNum--
		sbs = s.List()
		assert.Len(sbs, sbNum)

		t.Logf("get should return not exist error after deletion")
		sb, err := s.Get(truncID)
		assert.Equal(Sandbox{}, sb)
		assert.Equal(errdefs.ErrNotFound, err)
	}
}
