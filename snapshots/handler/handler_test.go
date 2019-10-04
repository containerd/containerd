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

package handler

import (
	"context"
	"fmt"
	"testing"

	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/snapshots"
	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/identity"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

const testRef = "testRef"

// TestSkippableCheck tests functionality to check if a snapshot is
// pull-skippable and to filter out the layer from download candidates.
func TestSkippableCheck(t *testing.T) {
	tests := []struct {
		name      string
		skippable []bool
	}{
		{
			name:      "download_all",
			skippable: []bool{false, false, false},
		},
		{
			name:      "all_skippable",
			skippable: []bool{true, true, true},
		},
		{
			name:      "lower_layer_skippable",
			skippable: []bool{true, true, false},
		},
		{
			name:      "upper_layer_skippable",
			skippable: []bool{false, true, false},
		},
		{
			name:      "partially_skippable_1",
			skippable: []bool{true, false, true, false},
		},
		{
			name:      "partially_skippable_2",
			skippable: []bool{false, true, false, true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var (
				skippable = make(map[string]bool)
				inDesc    []ocispec.Descriptor
				inChain   []digest.Digest
			)
			for i, e := range tt.skippable {
				desc := descriptor(i)
				inDesc = append(inDesc, desc)
				inChain = append(inChain, digest.Digest(fmt.Sprintf("%d-chain", i)))
				if e {
					skippable[desc.Digest.String()] = true
					t.Logf("  skippable: %v", desc.Digest.String())
				}
			}
			sn := &testSnapshotter{
				t:         t,
				skippable: skippable,
				active:    make(map[string]bool),
				committed: make(map[string]bool),
			}
			gotI := firstFalseIdx(inDesc, inChain, isPullSkippable(context.TODO(), sn, testRef))
			wantI := firstFalseIdx(inDesc, inChain, func(l ocispec.Descriptor, chain []digest.Digest) bool {
				return tt.skippable[len(chain)-1]
			})
			if gotI != wantI {
				t.Errorf("upper-most skippable = %d; want %d", gotI, wantI)
				return
			}
			if len(sn.active) != 0 {
				t.Errorf("remaining garbedge active snapshot. must bo clean")
				return
			}
			if len(sn.committed) != wantI {
				t.Errorf("num of the committed snapshot: %d; want: %d", len(sn.committed), wantI)
				return
			}
			for i := 0; i < wantI; i++ {
				chainID := identity.ChainID(inChain[:i+1]).String()
				if ok := sn.committed[chainID]; !ok {
					t.Errorf("chainID %q must be committed", chainID)
					return
				}
			}
		})
	}
}

// TestExeclude tests function to get a slice which has been excluded by another
// slice from original one.
func TestExclude(t *testing.T) {
	tests := []struct {
		name string
		a    []int
		b    []int
		want []int
	}{
		{
			name: "nothing",
			a:    nil,
			b:    []int{1, 2, 3},
			want: nil,
		},
		{
			name: "all_remain",
			a:    []int{1, 2, 3},
			b:    nil,
			want: []int{1, 2, 3},
		},
		{
			name: "all_remove",
			a:    []int{1, 2, 3},
			b:    []int{1, 2, 3},
			want: nil,
		},
		{
			name: "ordered",
			a:    []int{1, 2, 3, 4, 5},
			b:    []int{2, 4},
			want: []int{1, 3, 5},
		},
		{
			name: "random",
			a:    []int{5, 1, 3, 2, 4},
			b:    []int{4, 2},
			want: []int{5, 1, 3},
		},
		{
			name: "duplicated",
			a:    []int{5, 4, 5, 1, 2, 3, 1, 5},
			b:    []int{4, 1, 3},
			want: []int{5, 5, 2, 5},
		},
		{
			name: "unnecessary",
			a:    []int{1, 2, 3, 4},
			b:    []int{1, 7, 8, 2, 9},
			want: []int{3, 4},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := exclude(descSlice(tt.a), descSlice(tt.b))
			want := descSlice(tt.want)
			t.Logf("  result: %v", res)
			t.Logf("  wanted: %v", want)
			if len(res) != len(want) {
				t.Errorf("result slice size is %d; want %d", len(res), len(want))
			}
			for i := 0; i < len(res); i++ {
				if res[i].Digest.String() != want[i].Digest.String() {
					t.Errorf("result value is %q; want %q",
						res[i].Digest.String(), want[i].Digest.String())
				}
			}
		})
	}
}

func descSlice(ids []int) (desc []ocispec.Descriptor) {
	for _, id := range ids {
		desc = append(desc, descriptor(id))
	}
	return
}

func descriptor(id int) ocispec.Descriptor {
	return ocispec.Descriptor{
		Digest: digest.Digest(fmt.Sprintf("%d-desc", id)),
	}
}

type testSnapshotter struct {
	t         *testing.T
	skippable map[string]bool
	active    map[string]bool
	committed map[string]bool
}

func (ts *testSnapshotter) Stat(ctx context.Context, key string) (snapshots.Info, error) {
	if ok := ts.active[key]; ok {
		return snapshots.Info{}, nil
	}
	if ok := ts.committed[key]; ok {
		return snapshots.Info{}, nil
	}
	return snapshots.Info{}, errors.Wrapf(errdefs.ErrNotFound, "resource %q does not exist", key)
}

func (ts *testSnapshotter) Prepare(ctx context.Context, key, parent string, opts ...snapshots.Opt) ([]mount.Mount, error) {
	if parent != "" {
		if ok := ts.committed[parent]; !ok {
			return nil, errors.Wrapf(errdefs.ErrNotFound, "parent snapshot %q does not exist", parent)
		}
	}

	if err := ts.createSnapshot(opts...); err != nil {
		ts.t.Logf("failed to create snapshot: %q", err)
		ts.active[key] = true
		return nil, nil
	}

	return nil, errors.Wrapf(errdefs.ErrAlreadyExists, "created snapshot")
}

func (ts *testSnapshotter) createSnapshot(opts ...snapshots.Opt) error {
	var base snapshots.Info
	for _, opt := range opts {
		if err := opt(&base); err != nil {
			return err
		}
	}

	chainID, ok := base.Labels[TargetSnapshotLabel]
	if !ok {
		return fmt.Errorf("chainID is not passed")
	}
	ref, ok := base.Labels[TargetRefLabel]
	if !ok {
		return fmt.Errorf("image reference is not passed")
	}
	digest, ok := base.Labels[TargetDigestLabel]
	if !ok {
		return fmt.Errorf("image digest are not passed")
	}
	if ref != testRef {
		return fmt.Errorf("unknown ref")
	}
	if ok := ts.skippable[digest]; !ok {
		return fmt.Errorf("unknown digest")
	}
	ts.committed[chainID] = true

	return nil
}

func (ts *testSnapshotter) Remove(ctx context.Context, key string) error {
	delete(ts.active, key)
	delete(ts.committed, key)
	return nil
}

func (ts *testSnapshotter) Update(ctx context.Context, info snapshots.Info, fieldpaths ...string) (snapshots.Info, error) {
	return snapshots.Info{}, nil
}

func (ts *testSnapshotter) Usage(ctx context.Context, key string) (snapshots.Usage, error) {
	return snapshots.Usage{}, nil
}

func (ts *testSnapshotter) Mounts(ctx context.Context, key string) ([]mount.Mount, error) {
	return nil, nil
}

func (ts *testSnapshotter) View(ctx context.Context, key, parent string, opts ...snapshots.Opt) ([]mount.Mount, error) {
	return nil, nil
}

func (ts *testSnapshotter) Commit(ctx context.Context, name, key string, opts ...snapshots.Opt) error {
	return nil
}

func (ts *testSnapshotter) Walk(ctx context.Context, fn snapshots.WalkFunc, filters ...string) error {
	return nil
}

func (ts *testSnapshotter) Close() error {
	return nil
}
