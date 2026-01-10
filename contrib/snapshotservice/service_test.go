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

package snapshotservice

import (
	"context"
	"testing"

	snapshotsapi "github.com/containerd/containerd/api/services/snapshots/v1"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/core/snapshots"
)

// mockSnapshotter is a mock implementation of snapshots.Snapshotter
// that captures the options passed to Commit for testing.
type mockSnapshotter struct {
	commitOpts []snapshots.Opt
}

func (m *mockSnapshotter) Stat(ctx context.Context, key string) (snapshots.Info, error) {
	return snapshots.Info{}, nil
}

func (m *mockSnapshotter) Update(ctx context.Context, info snapshots.Info, fieldpaths ...string) (snapshots.Info, error) {
	return snapshots.Info{}, nil
}

func (m *mockSnapshotter) Usage(ctx context.Context, key string) (snapshots.Usage, error) {
	return snapshots.Usage{}, nil
}

func (m *mockSnapshotter) Mounts(ctx context.Context, key string) ([]mount.Mount, error) {
	return nil, nil
}

func (m *mockSnapshotter) Prepare(ctx context.Context, key, parent string, opts ...snapshots.Opt) ([]mount.Mount, error) {
	return nil, nil
}

func (m *mockSnapshotter) View(ctx context.Context, key, parent string, opts ...snapshots.Opt) ([]mount.Mount, error) {
	return nil, nil
}

func (m *mockSnapshotter) Commit(ctx context.Context, name, key string, opts ...snapshots.Opt) error {
	m.commitOpts = opts
	return nil
}

func (m *mockSnapshotter) Remove(ctx context.Context, key string) error {
	return nil
}

func (m *mockSnapshotter) Walk(ctx context.Context, fn snapshots.WalkFunc, filters ...string) error {
	return nil
}

func (m *mockSnapshotter) Close() error {
	return nil
}

// TestCommitParentOption verifies that the Parent field from CommitSnapshotRequest
// is correctly passed to the snapshotter via WithParent option.
func TestCommitParentOption(t *testing.T) {
	for _, tc := range []struct {
		name           string
		parent         string
		labels         map[string]string
		expectedParent string
		expectedLabels map[string]string
	}{
		{
			name:           "WithParent",
			parent:         "parent-snapshot",
			expectedParent: "parent-snapshot",
		},
		{
			name:           "WithoutParent",
			parent:         "",
			expectedParent: "",
		},
		{
			name:           "WithLabelsAndParent",
			parent:         "parent-snapshot",
			labels:         map[string]string{"test-label": "test-value"},
			expectedParent: "parent-snapshot",
			expectedLabels: map[string]string{"test-label": "test-value"},
		},
		{
			name:           "WithLabelsOnly",
			parent:         "",
			labels:         map[string]string{"key": "value"},
			expectedParent: "",
			expectedLabels: map[string]string{"key": "value"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := &mockSnapshotter{}
			svc := FromSnapshotter(mock)

			req := &snapshotsapi.CommitSnapshotRequest{
				Name:   "test-snapshot",
				Key:    "test-key",
				Parent: tc.parent,
				Labels: tc.labels,
			}

			_, err := svc.Commit(context.Background(), req)
			if err != nil {
				t.Fatalf("Commit failed: %v", err)
			}

			// Apply all opts to check the resulting Info
			info := &snapshots.Info{}
			for _, opt := range mock.commitOpts {
				if err := opt(info); err != nil {
					t.Fatalf("failed to apply opt: %v", err)
				}
			}

			if info.Parent != tc.expectedParent {
				t.Errorf("expected parent %q, got %q", tc.expectedParent, info.Parent)
			}

			if tc.expectedLabels != nil {
				for k, v := range tc.expectedLabels {
					if info.Labels[k] != v {
						t.Errorf("expected label %q=%q, got %q", k, v, info.Labels[k])
					}
				}
			}
		})
	}
}
