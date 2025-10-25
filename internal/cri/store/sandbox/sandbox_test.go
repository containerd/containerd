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

	"github.com/containerd/containerd/v2/internal/cri/store/label"
	"github.com/containerd/containerd/v2/internal/cri/store/stats"
	"github.com/containerd/errdefs"

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

// TestSandboxStoreDeleteWithCorruptedIndex tests that Delete removes sandbox
// from the store even when the truncated index is out of sync (issue #12390).
// This reproduces the production bug where pods leaked when idIndex lost track
// of sandbox IDs, causing the old Delete implementation to silently fail.
func TestSandboxStoreDeleteWithCorruptedIndex(t *testing.T) {
	labels := label.NewStore()
	s := NewStore(labels)

	// Create a test sandbox
	sandbox := NewSandbox(
		Metadata{
			ID:   "test-sandbox-12390",
			Name: "leak-test",
			Config: &runtime.PodSandboxConfig{
				Metadata: &runtime.PodSandboxMetadata{
					Name:      "TestPod",
					Uid:       "TestUid",
					Namespace: "TestNamespace",
					Attempt:   1,
				},
			},
		},
		Status{State: StateReady},
	)

	// Add sandbox to store (adds to both map and idIndex)
	assert := assertlib.New(t)
	assert.NoError(s.Add(sandbox))

	// Verify it's in the store
	_, err := s.Get(sandbox.ID)
	assert.NoError(err)
	assert.Len(s.List(), 1)

	// Simulate idIndex corruption by manually removing from index
	// This simulates the condition that caused the leak in production
	s.idIndex.Delete(sandbox.ID)

	// Verify idIndex no longer has the ID
	_, err = s.idIndex.Get(sandbox.ID)
	assert.Error(err, "idIndex should not have the ID after manual deletion")

	// But the sandbox should still be in the map
	assert.Len(s.List(), 1, "sandbox should still be in map")

	// Now call Delete - with the bug, this would silently fail
	// With the fix, it should delete from map even though idIndex lookup fails
	s.Delete(sandbox.ID)

	// Verify the sandbox was actually removed from the store
	assert.Len(s.List(), 0, "sandbox should be removed from map despite idIndex corruption")
	_, err = s.Get(sandbox.ID)
	assert.Equal(errdefs.ErrNotFound, err, "sandbox should not be found after deletion")
}
