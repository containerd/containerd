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

package storage

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/errdefs"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
)

type testFunc func(context.Context, *testing.T, *MetaStore)

type metaFactory func(string) (*MetaStore, error)

type populateFunc func(context.Context, *MetaStore) error

// MetaStoreSuite runs a test suite on the metastore given a factory function.
func MetaStoreSuite(t *testing.T, name string, meta func(root string) (*MetaStore, error)) {
	t.Run("GetInfo", makeTest(t, name, meta, inReadTransaction(testGetInfo, basePopulate)))
	t.Run("GetInfoNotExist", makeTest(t, name, meta, inReadTransaction(testGetInfoNotExist, basePopulate)))
	t.Run("GetInfoEmptyDB", makeTest(t, name, meta, inReadTransaction(testGetInfoNotExist, nil)))
	t.Run("Walk", makeTest(t, name, meta, inReadTransaction(testWalk, basePopulate)))
	t.Run("GetSnapshot", makeTest(t, name, meta, testGetSnapshot))
	t.Run("GetSnapshotNotExist", makeTest(t, name, meta, inReadTransaction(testGetSnapshotNotExist, basePopulate)))
	t.Run("GetSnapshotCommitted", makeTest(t, name, meta, inReadTransaction(testGetSnapshotCommitted, basePopulate)))
	t.Run("GetSnapshotEmptyDB", makeTest(t, name, meta, inReadTransaction(testGetSnapshotNotExist, basePopulate)))
	t.Run("CreateActive", makeTest(t, name, meta, inWriteTransaction(testCreateActive)))
	t.Run("CreateActiveNotExist", makeTest(t, name, meta, inWriteTransaction(testCreateActiveNotExist)))
	t.Run("CreateActiveExist", makeTest(t, name, meta, inWriteTransaction(testCreateActiveExist)))
	t.Run("CreateActiveFromActive", makeTest(t, name, meta, inWriteTransaction(testCreateActiveFromActive)))
	t.Run("Commit", makeTest(t, name, meta, inWriteTransaction(testCommit)))
	t.Run("CommitNotExist", makeTest(t, name, meta, inWriteTransaction(testCommitExist)))
	t.Run("CommitExist", makeTest(t, name, meta, inWriteTransaction(testCommitExist)))
	t.Run("CommitCommitted", makeTest(t, name, meta, inWriteTransaction(testCommitCommitted)))
	t.Run("CommitViewFails", makeTest(t, name, meta, inWriteTransaction(testCommitViewFails)))
	t.Run("Remove", makeTest(t, name, meta, inWriteTransaction(testRemove)))
	t.Run("RemoveNotExist", makeTest(t, name, meta, inWriteTransaction(testRemoveNotExist)))
	t.Run("RemoveWithChildren", makeTest(t, name, meta, inWriteTransaction(testRemoveWithChildren)))
	t.Run("ParentIDs", makeTest(t, name, meta, inWriteTransaction(testParents)))
	t.Run("Rebase", makeTest(t, name, meta, inWriteTransaction(testRebase)))
}

// makeTest creates a testsuite with a writable transaction
func makeTest(t *testing.T, name string, metaFn metaFactory, fn testFunc) func(t *testing.T) {
	return func(t *testing.T) {
		ctx := context.Background()

		ms, err := metaFn(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}

		t.Cleanup(func() {
			ms.Close()
		})

		fn(ctx, t, ms)
	}
}

func inReadTransaction(fn testFunc, pf populateFunc) testFunc {
	return func(ctx context.Context, t *testing.T, ms *MetaStore) {
		if pf != nil {
			ctx, tx, err := ms.TransactionContext(ctx, true)
			if err != nil {
				t.Fatal(err)
			}
			if err := pf(ctx, ms); err != nil {
				if rerr := tx.Rollback(); rerr != nil {
					t.Logf("Rollback failed: %+v", rerr)
				}
				t.Fatalf("Populate failed: %+v", err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatalf("Populate commit failed: %+v", err)
			}
		}

		ctx, tx, err := ms.TransactionContext(ctx, false)
		if err != nil {
			t.Fatalf("Failed start transaction: %+v", err)
		}
		defer func() {
			if err := tx.Rollback(); err != nil {
				t.Logf("Rollback failed: %+v", err)
				if !t.Failed() {
					t.FailNow()
				}
			}
		}()

		fn(ctx, t, ms)
	}
}

func inWriteTransaction(fn testFunc) testFunc {
	return func(ctx context.Context, t *testing.T, ms *MetaStore) {
		ctx, tx, err := ms.TransactionContext(ctx, true)
		if err != nil {
			t.Fatalf("Failed to start transaction: %+v", err)
		}
		defer func() {
			if t.Failed() {
				if err := tx.Rollback(); err != nil {
					t.Logf("Rollback failed: %+v", err)
				}
			} else {
				if err := tx.Commit(); err != nil {
					t.Fatalf("Commit failed: %+v", err)
				}
			}
		}()
		fn(ctx, t, ms)
	}
}

// basePopulate creates 7 snapshots
// - "committed-1": committed without parent
// - "committed-2":  committed with parent "committed-1"
// - "active-1": active without parent
// - "active-2": active with parent "committed-1"
// - "active-3": active with parent "committed-2"
// - "active-4": readonly active without parent"
// - "active-5": readonly active with parent "committed-2"
func basePopulate(ctx context.Context, ms *MetaStore) error {
	if _, err := CreateSnapshot(ctx, snapshots.KindActive, "committed-tmp-1", ""); err != nil {
		return fmt.Errorf("failed to create active: %w", err)
	}
	if _, err := CommitActive(ctx, "committed-tmp-1", "committed-1", snapshots.Usage{Size: 1}); err != nil {
		return fmt.Errorf("failed to create active: %w", err)
	}
	if _, err := CreateSnapshot(ctx, snapshots.KindActive, "committed-tmp-2", "committed-1"); err != nil {
		return fmt.Errorf("failed to create active: %w", err)
	}
	if _, err := CommitActive(ctx, "committed-tmp-2", "committed-2", snapshots.Usage{Size: 2}); err != nil {
		return fmt.Errorf("failed to create active: %w", err)
	}
	if _, err := CreateSnapshot(ctx, snapshots.KindActive, "active-1", ""); err != nil {
		return fmt.Errorf("failed to create active: %w", err)
	}
	if _, err := CreateSnapshot(ctx, snapshots.KindActive, "active-2", "committed-1"); err != nil {
		return fmt.Errorf("failed to create active: %w", err)
	}
	if _, err := CreateSnapshot(ctx, snapshots.KindActive, "active-3", "committed-2"); err != nil {
		return fmt.Errorf("failed to create active: %w", err)
	}
	if _, err := CreateSnapshot(ctx, snapshots.KindView, "view-1", ""); err != nil {
		return fmt.Errorf("failed to create active: %w", err)
	}
	if _, err := CreateSnapshot(ctx, snapshots.KindView, "view-2", "committed-2"); err != nil {
		return fmt.Errorf("failed to create active: %w", err)
	}
	return nil
}

var baseInfo = map[string]snapshots.Info{
	"committed-1": {
		Name:   "committed-1",
		Parent: "",
		Kind:   snapshots.KindCommitted,
	},
	"committed-2": {
		Name:   "committed-2",
		Parent: "committed-1",
		Kind:   snapshots.KindCommitted,
	},
	"active-1": {
		Name:   "active-1",
		Parent: "",
		Kind:   snapshots.KindActive,
	},
	"active-2": {
		Name:   "active-2",
		Parent: "committed-1",
		Kind:   snapshots.KindActive,
	},
	"active-3": {
		Name:   "active-3",
		Parent: "committed-2",
		Kind:   snapshots.KindActive,
	},
	"view-1": {
		Name:   "view-1",
		Parent: "",
		Kind:   snapshots.KindView,
	},
	"view-2": {
		Name:   "view-2",
		Parent: "committed-2",
		Kind:   snapshots.KindView,
	},
}

func assertNotExist(t *testing.T, err error) {
	t.Helper()
	assert.True(t, errdefs.IsNotFound(err), "got %+v", err)
}

func assertNotActive(t *testing.T, err error) {
	t.Helper()
	assert.True(t, errdefs.IsFailedPrecondition(err), "got %+v", err)
}

func assertNotCommitted(t *testing.T, err error) {
	t.Helper()
	assert.True(t, errdefs.IsInvalidArgument(err), "got %+v", err)
}

func assertExist(t *testing.T, err error) {
	t.Helper()
	assert.True(t, errdefs.IsAlreadyExists(err), "got %+v", err)
}

func testGetInfo(ctx context.Context, t *testing.T, _ *MetaStore) {
	for key, expected := range baseInfo {
		_, info, _, err := GetInfo(ctx, key)
		assert.Nil(t, err, "on key %v", key)
		assert.Truef(t, cmp.Equal(expected, info, cmpSnapshotInfo), "on key %v", key)
	}
}

// compare snapshot.Info Updated and Created fields by checking they are
// within a threshold of time.Now()
var cmpSnapshotInfo = cmp.FilterPath(
	func(path cmp.Path) bool {
		field := path.Last().String()
		return field == ".Created" || field == ".Updated"
	},
	cmp.Comparer(func(expected, actual time.Time) bool {
		// cmp.Options must be symmetric, so swap the args
		if actual.IsZero() {
			actual, expected = expected, actual
		}
		if !expected.IsZero() {
			return false
		}
		// actual value should be within a few seconds of now
		now := time.Now()
		delta := now.Sub(actual)
		threshold := 30 * time.Second
		return delta > -threshold && delta < threshold
	}))

func testGetInfoNotExist(ctx context.Context, t *testing.T, _ *MetaStore) {
	_, _, _, err := GetInfo(ctx, "active-not-exist")
	assertNotExist(t, err)
}

func testWalk(ctx context.Context, t *testing.T, _ *MetaStore) {
	found := map[string]snapshots.Info{}
	err := WalkInfo(ctx, func(ctx context.Context, info snapshots.Info) error {
		if _, ok := found[info.Name]; ok {
			return errors.New("entry already encountered")
		}
		found[info.Name] = info
		return nil
	})
	assert.NoError(t, err)
	assert.True(t, cmp.Equal(baseInfo, found, cmpSnapshotInfo))
}

func testGetSnapshot(ctx context.Context, t *testing.T, ms *MetaStore) {
	snapshotMap := map[string]Snapshot{}
	populate := func(ctx context.Context, ms *MetaStore) error {
		if _, err := CreateSnapshot(ctx, snapshots.KindActive, "committed-tmp-1", ""); err != nil {
			return fmt.Errorf("failed to create active: %w", err)
		}
		if _, err := CommitActive(ctx, "committed-tmp-1", "committed-1", snapshots.Usage{}); err != nil {
			return fmt.Errorf("failed to create active: %w", err)
		}

		for _, opts := range []struct {
			Kind   snapshots.Kind
			Name   string
			Parent string
		}{
			{
				Name: "active-1",
				Kind: snapshots.KindActive,
			},
			{
				Name:   "active-2",
				Parent: "committed-1",
				Kind:   snapshots.KindActive,
			},
			{
				Name: "view-1",
				Kind: snapshots.KindView,
			},
			{
				Name:   "view-2",
				Parent: "committed-1",
				Kind:   snapshots.KindView,
			},
		} {
			active, err := CreateSnapshot(ctx, opts.Kind, opts.Name, opts.Parent)
			if err != nil {
				return fmt.Errorf("failed to create active: %w", err)
			}
			snapshotMap[opts.Name] = active
		}
		return nil
	}

	test := func(ctx context.Context, t *testing.T, ms *MetaStore) {
		for key, expected := range snapshotMap {
			s, err := GetSnapshot(ctx, key)
			assert.Nil(t, err, "failed to get snapshot %s", key)
			assert.Equalf(t, expected, s, "on key %s", key)
		}
	}

	inReadTransaction(test, populate)(ctx, t, ms)
}

func testGetSnapshotCommitted(ctx context.Context, t *testing.T, ms *MetaStore) {
	_, err := GetSnapshot(ctx, "committed-1")
	assertNotActive(t, err)
}

func testGetSnapshotNotExist(ctx context.Context, t *testing.T, ms *MetaStore) {
	_, err := GetSnapshot(ctx, "active-not-exist")
	assertNotExist(t, err)
}

func testCreateActive(ctx context.Context, t *testing.T, ms *MetaStore) {
	a1, err := CreateSnapshot(ctx, snapshots.KindActive, "active-1", "")
	if err != nil {
		t.Fatal(err)
	}
	if a1.Kind != snapshots.KindActive {
		t.Fatal("Expected writable active")
	}

	a2, err := CreateSnapshot(ctx, snapshots.KindView, "view-1", "")
	if err != nil {
		t.Fatal(err)
	}
	if a2.ID == a1.ID {
		t.Fatal("Returned active identifiers must be unique")
	}
	if a2.Kind != snapshots.KindView {
		t.Fatal("Expected a view")
	}

	commitID, err := CommitActive(ctx, "active-1", "committed-1", snapshots.Usage{})
	if err != nil {
		t.Fatal(err)
	}
	if commitID != a1.ID {
		t.Fatal("Snapshot identifier must not change on commit")
	}

	a3, err := CreateSnapshot(ctx, snapshots.KindActive, "active-3", "committed-1")
	if err != nil {
		t.Fatal(err)
	}
	if a3.ID == a1.ID {
		t.Fatal("Returned active identifiers must be unique")
	}
	if len(a3.ParentIDs) != 1 {
		t.Fatalf("Expected 1 parent, got %d", len(a3.ParentIDs))
	}
	if a3.ParentIDs[0] != commitID {
		t.Fatal("Expected active parent to be same as commit ID")
	}
	if a3.Kind != snapshots.KindActive {
		t.Fatal("Expected writable active")
	}

	a4, err := CreateSnapshot(ctx, snapshots.KindView, "view-2", "committed-1")
	if err != nil {
		t.Fatal(err)
	}
	if a4.ID == a1.ID {
		t.Fatal("Returned active identifiers must be unique")
	}
	if len(a3.ParentIDs) != 1 {
		t.Fatalf("Expected 1 parent, got %d", len(a3.ParentIDs))
	}
	if a3.ParentIDs[0] != commitID {
		t.Fatal("Expected active parent to be same as commit ID")
	}
	if a4.Kind != snapshots.KindView {
		t.Fatal("Expected a view")
	}
}

func testCreateActiveExist(ctx context.Context, t *testing.T, ms *MetaStore) {
	if err := basePopulate(ctx, ms); err != nil {
		t.Fatalf("Populate failed: %+v", err)
	}
	_, err := CreateSnapshot(ctx, snapshots.KindActive, "active-1", "")
	assertExist(t, err)
	_, err = CreateSnapshot(ctx, snapshots.KindActive, "committed-1", "")
	assertExist(t, err)
}

func testCreateActiveNotExist(ctx context.Context, t *testing.T, ms *MetaStore) {
	_, err := CreateSnapshot(ctx, snapshots.KindActive, "active-1", "does-not-exist")
	assertNotExist(t, err)
}

func testCreateActiveFromActive(ctx context.Context, t *testing.T, ms *MetaStore) {
	if err := basePopulate(ctx, ms); err != nil {
		t.Fatalf("Populate failed: %+v", err)
	}
	_, err := CreateSnapshot(ctx, snapshots.KindActive, "active-new", "active-1")
	assertNotCommitted(t, err)
}

func testCommit(ctx context.Context, t *testing.T, ms *MetaStore) {
	a1, err := CreateSnapshot(ctx, snapshots.KindActive, "active-1", "")
	if err != nil {
		t.Fatal(err)
	}
	if a1.Kind != snapshots.KindActive {
		t.Fatal("Expected writable active")
	}

	commitID, err := CommitActive(ctx, "active-1", "committed-1", snapshots.Usage{})
	if err != nil {
		t.Fatal(err)
	}
	if commitID != a1.ID {
		t.Fatal("Snapshot identifier must not change on commit")
	}

	_, err = GetSnapshot(ctx, "active-1")
	assertNotExist(t, err)
	_, err = GetSnapshot(ctx, "committed-1")
	assertNotActive(t, err)
}

func testCommitExist(ctx context.Context, t *testing.T, ms *MetaStore) {
	if err := basePopulate(ctx, ms); err != nil {
		t.Fatalf("Populate failed: %+v", err)
	}
	_, err := CommitActive(ctx, "active-1", "committed-1", snapshots.Usage{})
	assertExist(t, err)
}

func testCommitCommitted(ctx context.Context, t *testing.T, ms *MetaStore) {
	if err := basePopulate(ctx, ms); err != nil {
		t.Fatalf("Populate failed: %+v", err)
	}
	_, err := CommitActive(ctx, "committed-1", "committed-3", snapshots.Usage{})
	assertNotActive(t, err)
}

func testCommitViewFails(ctx context.Context, t *testing.T, ms *MetaStore) {
	if err := basePopulate(ctx, ms); err != nil {
		t.Fatalf("Populate failed: %+v", err)
	}
	_, err := CommitActive(ctx, "view-1", "committed-3", snapshots.Usage{})
	if err == nil {
		t.Fatal("Expected error committing readonly active")
	}
}

func testRemove(ctx context.Context, t *testing.T, ms *MetaStore) {
	a1, err := CreateSnapshot(ctx, snapshots.KindActive, "active-1", "")
	if err != nil {
		t.Fatal(err)
	}

	commitID, err := CommitActive(ctx, "active-1", "committed-1", snapshots.Usage{})
	if err != nil {
		t.Fatal(err)
	}
	if commitID != a1.ID {
		t.Fatal("Snapshot identifier must not change on commit")
	}

	a2, err := CreateSnapshot(ctx, snapshots.KindView, "view-1", "committed-1")
	if err != nil {
		t.Fatal(err)
	}

	a3, err := CreateSnapshot(ctx, snapshots.KindView, "view-2", "committed-1")
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = Remove(ctx, "active-1")
	assertNotExist(t, err)

	r3, k3, err := Remove(ctx, "view-2")
	if err != nil {
		t.Fatal(err)
	}
	if r3 != a3.ID {
		t.Fatal("Expected remove ID to match create ID")
	}
	if k3 != snapshots.KindView {
		t.Fatalf("Expected view kind, got %v", k3)
	}

	r2, k2, err := Remove(ctx, "view-1")
	if err != nil {
		t.Fatal(err)
	}
	if r2 != a2.ID {
		t.Fatal("Expected remove ID to match create ID")
	}
	if k2 != snapshots.KindView {
		t.Fatalf("Expected view kind, got %v", k2)
	}

	r1, k1, err := Remove(ctx, "committed-1")
	if err != nil {
		t.Fatal(err)
	}
	if r1 != commitID {
		t.Fatal("Expected remove ID to match commit ID")
	}
	if k1 != snapshots.KindCommitted {
		t.Fatalf("Expected committed kind, got %v", k1)
	}
}

func testRemoveWithChildren(ctx context.Context, t *testing.T, ms *MetaStore) {
	if err := basePopulate(ctx, ms); err != nil {
		t.Fatalf("Populate failed: %+v", err)
	}
	_, _, err := Remove(ctx, "committed-1")
	if err == nil {
		t.Fatalf("Expected removal of snapshot with children to error")
	}
	_, _, err = Remove(ctx, "committed-1")
	if err == nil {
		t.Fatalf("Expected removal of snapshot with children to error")
	}
}

func testRemoveNotExist(ctx context.Context, t *testing.T, _ *MetaStore) {
	_, _, err := Remove(ctx, "does-not-exist")
	assertNotExist(t, err)
}

func testParents(ctx context.Context, t *testing.T, ms *MetaStore) {
	if err := basePopulate(ctx, ms); err != nil {
		t.Fatalf("Populate failed: %+v", err)
	}

	testcases := []struct {
		Name    string
		Parents int
	}{
		{"committed-1", 0},
		{"committed-2", 1},
		{"active-1", 0},
		{"active-2", 1},
		{"active-3", 2},
		{"view-1", 0},
		{"view-2", 2},
	}

	for _, tc := range testcases {
		name := tc.Name
		expectedID := ""
		expectedParents := []string{}
		for i := tc.Parents; i >= 0; i-- {
			sid, info, _, err := GetInfo(ctx, name)
			if err != nil {
				t.Fatalf("Failed to get snapshot %s: %v", tc.Name, err)
			}
			var (
				id      string
				parents []string
			)
			if info.Kind == snapshots.KindCommitted {
				// When committed, create view and resolve from view
				nid := fmt.Sprintf("test-%s-%d", tc.Name, i)
				s, err := CreateSnapshot(ctx, snapshots.KindView, nid, name)
				if err != nil {
					t.Fatalf("Failed to get snapshot %s: %v", tc.Name, err)
				}
				if len(s.ParentIDs) != i+1 {
					t.Fatalf("Unexpected number of parents for view of %s: %d, expected %d", name, len(s.ParentIDs), i+1)
				}
				id = s.ParentIDs[0]
				parents = s.ParentIDs[1:]
			} else {
				s, err := GetSnapshot(ctx, name)
				if err != nil {
					t.Fatalf("Failed to get snapshot %s: %v", tc.Name, err)
				}
				if len(s.ParentIDs) != i {
					t.Fatalf("Unexpected number of parents for %s: %d, expected %d", name, len(s.ParentIDs), i)
				}

				id = s.ID
				parents = s.ParentIDs
			}
			if sid != id {
				t.Fatalf("Info ID mismatched resolved snapshot ID for %s, %s vs %s", name, sid, id)
			}

			if expectedID != "" {
				if id != expectedID {
					t.Errorf("Unexpected ID of parent: %s, expected %s", id, expectedID)
				}
			}

			if len(expectedParents) > 0 {
				for j := range expectedParents {
					if parents[j] != expectedParents[j] {
						t.Errorf("Unexpected ID in parent array at %d: %s, expected %s", j, parents[j], expectedParents[j])
					}
				}
			}

			if i > 0 {
				name = info.Parent
				expectedID = parents[0]
				expectedParents = parents[1:]
			}

		}
	}
}

func testRebase(ctx context.Context, t *testing.T, ms *MetaStore) {
	if err := basePopulate(ctx, ms); err != nil {
		t.Fatalf("Populate failed: %+v", err)
	}

	testcases := []struct {
		Key       string
		Name      string
		OldParent string
		NewParent string
		ExpectErr bool
	}{
		{"rebase-active-1", "rebase-committed-1", "", "committed-1", false},
		{"rebase-active-2", "rebase-committed-2", "committed-1", "committed-1", false},
		{"rebase-active-3", "rebase-committed-3", "", "nonexist", true},
		{"rebase-active-4", "rebase-committed-4", "committed-1", "", false},
		{"rebase-active-5", "rebase-committed-5", "", "active-1", true},
	}

	for _, tc := range testcases {
		_, err := CreateSnapshot(ctx, snapshots.KindActive, tc.Key, tc.OldParent)
		if err != nil {
			t.Fatal(err)
		}
		id, err := CommitActive(ctx, tc.Key, tc.Name, snapshots.Usage{}, snapshots.WithParent(tc.NewParent))
		if tc.ExpectErr {
			if err == nil {
				t.Fatalf("Expected rebase of %s to fail", tc.Key)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		view := fmt.Sprintf("%s-view", tc.Name)
		_, err = CreateSnapshot(ctx, snapshots.KindView, view, tc.Name)
		if err != nil {
			t.Fatal(err)
		}
		parent := tc.NewParent
		if len(parent) == 0 {
			parent = tc.OldParent
		}
		pid, _, _, err := GetInfo(ctx, parent)
		if err != nil {
			t.Fatal(err)
		}
		s, err := GetSnapshot(ctx, view)
		if err != nil {
			t.Fatal(err)
		}
		if len(s.ParentIDs) != 2 || s.ParentIDs[0] != id || s.ParentIDs[1] != pid {
			t.Fatalf("Unexpected parent IDs for %s: %+v", tc.Name, s.ParentIDs)
		}
		_, _, err = Remove(ctx, tc.Name)
		if err == nil {
			t.Fatalf("Expected removal of parent %s to fail", tc.Name)
		}
	}
}
