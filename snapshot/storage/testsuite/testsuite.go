package testsuite

import (
	"context"
	"io/ioutil"
	"os"
	"testing"

	"github.com/docker/containerd/snapshot"
	"github.com/docker/containerd/snapshot/storage"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
)

type testFunc func(context.Context, *testing.T, storage.MetaStore)

type metaFactory func(context.Context, string) (storage.MetaStore, error)

type populateFunc func(context.Context, storage.MetaStore) error

// MetaStoreSuite runs a test suite on the metastore given a factory function.
func MetaStoreSuite(t *testing.T, name string, meta func(ctx context.Context, root string) (storage.MetaStore, error)) {
	t.Run("Stat", makeTest(t, name, meta, inReadTransaction(testStat, basePopulate)))
	t.Run("StatNotExist", makeTest(t, name, meta, inReadTransaction(testStatNotExist, basePopulate)))
	t.Run("StatEmptyDB", makeTest(t, name, meta, inReadTransaction(testStatNotExist, nil)))
	t.Run("Walk", makeTest(t, name, meta, inReadTransaction(testWalk, basePopulate)))
	t.Run("GetActive", makeTest(t, name, meta, testGetActive))
	t.Run("GetActiveNotExist", makeTest(t, name, meta, inReadTransaction(testGetActiveNotExist, basePopulate)))
	t.Run("GetActiveCommitted", makeTest(t, name, meta, inReadTransaction(testGetActiveCommitted, basePopulate)))
	t.Run("GetActiveEmptyDB", makeTest(t, name, meta, inReadTransaction(testGetActiveNotExist, basePopulate)))
	t.Run("CreateActive", makeTest(t, name, meta, inWriteTransaction(testCreateActive)))
	t.Run("CreateActiveNotExist", makeTest(t, name, meta, inWriteTransaction(testCreateActiveNotExist)))
	t.Run("CreateActiveExist", makeTest(t, name, meta, inWriteTransaction(testCreateActiveExist)))
	t.Run("CreateActiveFromActive", makeTest(t, name, meta, inWriteTransaction(testCreateActiveFromActive)))
	t.Run("Commit", makeTest(t, name, meta, inWriteTransaction(testCommit)))
	t.Run("CommitNotExist", makeTest(t, name, meta, inWriteTransaction(testCommitExist)))
	t.Run("CommitExist", makeTest(t, name, meta, inWriteTransaction(testCommitExist)))
	t.Run("CommitCommitted", makeTest(t, name, meta, inWriteTransaction(testCommitCommitted)))
	t.Run("CommitReadonly", makeTest(t, name, meta, inWriteTransaction(testCommitReadonly)))
	t.Run("Remove", makeTest(t, name, meta, inWriteTransaction(testRemove)))
	t.Run("RemoveNotExist", makeTest(t, name, meta, inWriteTransaction(testRemoveNotExist)))
	t.Run("RemoveWithChildren", makeTest(t, name, meta, inWriteTransaction(testRemoveWithChildren)))
}

// makeTest creates a testsuite with a writable transaction
func makeTest(t *testing.T, name string, metaFn metaFactory, fn testFunc) func(t *testing.T) {
	return func(t *testing.T) {
		ctx := context.Background()
		tmpDir, err := ioutil.TempDir("", "metastore-test-"+name+"-")
		if err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(tmpDir)

		ms, err := metaFn(ctx, tmpDir)
		if err != nil {
			t.Fatal(err)
		}

		fn(ctx, t, ms)
	}
}

func inReadTransaction(fn testFunc, pf populateFunc) testFunc {
	return func(ctx context.Context, t *testing.T, ms storage.MetaStore) {
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
			t.Fatal("Failed start transaction: %+v", err)
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
	return func(ctx context.Context, t *testing.T, ms storage.MetaStore) {
		ctx, tx, err := ms.TransactionContext(ctx, true)
		if err != nil {
			t.Fatal("Failed to start transaction: %+v", err)
		}
		defer func() {
			if t.Failed() {
				if err := tx.Rollback(); err != nil {
					t.Logf("Rollback failed: %+v", err)
				}
			} else {
				if err := tx.Commit(); err != nil {
					t.Fatal("Commit failed: %+v", err)
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
func basePopulate(ctx context.Context, ms storage.MetaStore) error {
	if _, err := ms.CreateActive(ctx, "committed-tmp-1", "", false); err != nil {
		return errors.Wrap(err, "failed to create active")
	}
	if _, err := ms.Commit(ctx, "committed-tmp-1", "committed-1"); err != nil {
		return errors.Wrap(err, "failed to create active")
	}
	if _, err := ms.CreateActive(ctx, "committed-tmp-2", "committed-1", false); err != nil {
		return errors.Wrap(err, "failed to create active")
	}
	if _, err := ms.Commit(ctx, "committed-tmp-2", "committed-2"); err != nil {
		return errors.Wrap(err, "failed to create active")
	}
	if _, err := ms.CreateActive(ctx, "active-1", "", false); err != nil {
		return errors.Wrap(err, "failed to create active")
	}
	if _, err := ms.CreateActive(ctx, "active-2", "committed-1", false); err != nil {
		return errors.Wrap(err, "failed to create active")
	}
	if _, err := ms.CreateActive(ctx, "active-3", "committed-2", false); err != nil {
		return errors.Wrap(err, "failed to create active")
	}
	if _, err := ms.CreateActive(ctx, "active-4", "", true); err != nil {
		return errors.Wrap(err, "failed to create active")
	}
	if _, err := ms.CreateActive(ctx, "active-5", "committed-2", true); err != nil {
		return errors.Wrap(err, "failed to create active")
	}
	return nil
}

var baseInfo = map[string]snapshot.Info{
	"committed-1": {
		Name:     "committed-1",
		Parent:   "",
		Kind:     snapshot.KindCommitted,
		Readonly: true,
	},
	"committed-2": {
		Name:     "committed-2",
		Parent:   "committed-1",
		Kind:     snapshot.KindCommitted,
		Readonly: true,
	},
	"active-1": {
		Name:     "active-1",
		Parent:   "",
		Kind:     snapshot.KindActive,
		Readonly: false,
	},
	"active-2": {
		Name:     "active-2",
		Parent:   "committed-1",
		Kind:     snapshot.KindActive,
		Readonly: false,
	},
	"active-3": {
		Name:     "active-3",
		Parent:   "committed-2",
		Kind:     snapshot.KindActive,
		Readonly: false,
	},
	"active-4": {
		Name:     "active-4",
		Parent:   "",
		Kind:     snapshot.KindActive,
		Readonly: true,
	},
	"active-5": {
		Name:     "active-5",
		Parent:   "committed-2",
		Kind:     snapshot.KindActive,
		Readonly: true,
	},
}

func assertNotExist(t *testing.T, err error) {
	if err == nil {
		t.Fatal("Expected not exist error")
	}
	if !snapshot.IsNotExist(err) {
		t.Fatalf("Expected not exist error, got %+v", err)
	}
}

func assertNotActive(t *testing.T, err error) {
	if err == nil {
		t.Fatal("Expected not active error")
	}
	if !snapshot.IsNotActive(err) {
		t.Fatalf("Expected not active error, got %+v", err)
	}
}

func assertNotCommitted(t *testing.T, err error) {
	if err == nil {
		t.Fatal("Expected active error")
	}
	if !snapshot.IsNotCommitted(err) {
		t.Fatalf("Expected active error, got %+v", err)
	}
}

func assertExist(t *testing.T, err error) {
	if err == nil {
		t.Fatal("Expected exist error")
	}
	if !snapshot.IsExist(err) {
		t.Fatalf("Expected exist error, got %+v", err)
	}
}

func testStat(ctx context.Context, t *testing.T, ms storage.MetaStore) {
	for key, expected := range baseInfo {
		info, err := ms.Stat(ctx, key)
		if err != nil {
			t.Fatalf("Stat on %v failed: %+v", key, err)
		}
		assert.Equal(t, expected, info)
	}
}

func testStatNotExist(ctx context.Context, t *testing.T, ms storage.MetaStore) {
	_, err := ms.Stat(ctx, "active-not-exist")
	assertNotExist(t, err)
}

func testWalk(ctx context.Context, t *testing.T, ms storage.MetaStore) {
	found := map[string]snapshot.Info{}
	err := ms.Walk(ctx, func(ctx context.Context, info snapshot.Info) error {
		if _, ok := found[info.Name]; ok {
			return errors.Errorf("entry already encountered")
		}
		found[info.Name] = info
		return nil
	})
	if err != nil {
		t.Fatalf("Walk failed: %+v", err)
	}
	assert.Equal(t, baseInfo, found)
}

func testGetActive(ctx context.Context, t *testing.T, ms storage.MetaStore) {
	activeMap := map[string]storage.Active{}
	populate := func(ctx context.Context, ms storage.MetaStore) error {
		if _, err := ms.CreateActive(ctx, "committed-tmp-1", "", false); err != nil {
			return errors.Wrap(err, "failed to create active")
		}
		if _, err := ms.Commit(ctx, "committed-tmp-1", "committed-1"); err != nil {
			return errors.Wrap(err, "failed to create active")
		}

		for _, opts := range []struct {
			Name     string
			Parent   string
			Readonly bool
		}{
			{
				Name: "active-1",
			},
			{
				Name:   "active-2",
				Parent: "committed-1",
			},
			{
				Name:     "active-3",
				Readonly: true,
			},
			{
				Name:     "active-4",
				Parent:   "committed-1",
				Readonly: true,
			},
		} {
			active, err := ms.CreateActive(ctx, opts.Name, opts.Parent, opts.Readonly)
			if err != nil {
				return errors.Wrap(err, "failed to create active")
			}
			activeMap[opts.Name] = active
		}
		return nil
	}

	test := func(ctx context.Context, t *testing.T, ms storage.MetaStore) {
		for key, expected := range activeMap {
			active, err := ms.GetActive(ctx, key)
			if err != nil {
				t.Fatal("Failed to get active: %+v", err)
			}
			assert.Equal(t, expected, active)
		}
	}

	inReadTransaction(test, populate)(ctx, t, ms)
}

func testGetActiveCommitted(ctx context.Context, t *testing.T, ms storage.MetaStore) {
	_, err := ms.GetActive(ctx, "committed-1")
	assertNotActive(t, err)
}

func testGetActiveNotExist(ctx context.Context, t *testing.T, ms storage.MetaStore) {
	_, err := ms.GetActive(ctx, "active-not-exist")
	assertNotExist(t, err)
}

func testCreateActive(ctx context.Context, t *testing.T, ms storage.MetaStore) {
	a1, err := ms.CreateActive(ctx, "active-1", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if a1.Readonly {
		t.Fatal("Expected writable active")
	}

	a2, err := ms.CreateActive(ctx, "active-2", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if a2.ID == a1.ID {
		t.Fatal("Returned active identifiers must be unique")
	}
	if !a2.Readonly {
		t.Fatal("Expected readonly active")
	}

	commitID, err := ms.Commit(ctx, "active-1", "committed-1")
	if err != nil {
		t.Fatal(err)
	}
	if commitID != a1.ID {
		t.Fatal("Snapshot identifier must not change on commit")
	}

	a3, err := ms.CreateActive(ctx, "active-3", "committed-1", false)
	if err != nil {
		t.Fatal(err)
	}
	if a3.ID == a1.ID {
		t.Fatal("Returned active identifiers must be unique")
	}
	if len(a3.ParentIDs) != 1 {
		t.Fatal("Expected 1 parent, got %d", len(a3.ParentIDs))
	}
	if a3.ParentIDs[0] != commitID {
		t.Fatal("Expected active parent to be same as commit ID")
	}
	if a3.Readonly {
		t.Fatal("Expected writable active")
	}

	a4, err := ms.CreateActive(ctx, "active-4", "committed-1", true)
	if err != nil {
		t.Fatal(err)
	}
	if a4.ID == a1.ID {
		t.Fatal("Returned active identifiers must be unique")
	}
	if len(a3.ParentIDs) != 1 {
		t.Fatal("Expected 1 parent, got %d", len(a3.ParentIDs))
	}
	if a3.ParentIDs[0] != commitID {
		t.Fatal("Expected active parent to be same as commit ID")
	}
	if !a4.Readonly {
		t.Fatal("Expected readonly active")
	}
}

func testCreateActiveExist(ctx context.Context, t *testing.T, ms storage.MetaStore) {
	if err := basePopulate(ctx, ms); err != nil {
		t.Fatalf("Populate failed: %+v", err)
	}
	_, err := ms.CreateActive(ctx, "active-1", "", false)
	assertExist(t, err)
	_, err = ms.CreateActive(ctx, "committed-1", "", false)
	assertExist(t, err)
}

func testCreateActiveNotExist(ctx context.Context, t *testing.T, ms storage.MetaStore) {
	_, err := ms.CreateActive(ctx, "active-1", "does-not-exist", false)
	assertNotExist(t, err)
}

func testCreateActiveFromActive(ctx context.Context, t *testing.T, ms storage.MetaStore) {
	if err := basePopulate(ctx, ms); err != nil {
		t.Fatalf("Populate failed: %+v", err)
	}
	_, err := ms.CreateActive(ctx, "active-new", "active-1", false)
	assertNotCommitted(t, err)
}

func testCommit(ctx context.Context, t *testing.T, ms storage.MetaStore) {
	a1, err := ms.CreateActive(ctx, "active-1", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if a1.Readonly {
		t.Fatal("Expected writable active")
	}

	commitID, err := ms.Commit(ctx, "active-1", "committed-1")
	if err != nil {
		t.Fatal(err)
	}
	if commitID != a1.ID {
		t.Fatal("Snapshot identifier must not change on commit")
	}

	_, err = ms.GetActive(ctx, "active-1")
	assertNotExist(t, err)
	_, err = ms.GetActive(ctx, "committed-1")
	assertNotActive(t, err)
}

func testCommitNotExist(ctx context.Context, t *testing.T, ms storage.MetaStore) {
	_, err := ms.Commit(ctx, "active-not-exist", "committed-1")
	assertNotExist(t, err)
}

func testCommitExist(ctx context.Context, t *testing.T, ms storage.MetaStore) {
	if err := basePopulate(ctx, ms); err != nil {
		t.Fatalf("Populate failed: %+v", err)
	}
	_, err := ms.Commit(ctx, "active-1", "committed-1")
	assertExist(t, err)
}

func testCommitCommitted(ctx context.Context, t *testing.T, ms storage.MetaStore) {
	if err := basePopulate(ctx, ms); err != nil {
		t.Fatalf("Populate failed: %+v", err)
	}
	_, err := ms.Commit(ctx, "committed-1", "committed-3")
	assertNotActive(t, err)
}

func testCommitReadonly(ctx context.Context, t *testing.T, ms storage.MetaStore) {
	if err := basePopulate(ctx, ms); err != nil {
		t.Fatalf("Populate failed: %+v", err)
	}
	_, err := ms.Commit(ctx, "active-5", "committed-3")
	if err == nil {
		t.Fatal("Expected error committing readonly active")
	}
}

func testRemove(ctx context.Context, t *testing.T, ms storage.MetaStore) {
	a1, err := ms.CreateActive(ctx, "active-1", "", false)
	if err != nil {
		t.Fatal(err)
	}

	commitID, err := ms.Commit(ctx, "active-1", "committed-1")
	if err != nil {
		t.Fatal(err)
	}
	if commitID != a1.ID {
		t.Fatal("Snapshot identifier must not change on commit")
	}

	a2, err := ms.CreateActive(ctx, "active-2", "committed-1", true)
	if err != nil {
		t.Fatal(err)
	}

	a3, err := ms.CreateActive(ctx, "active-3", "committed-1", true)
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = ms.Remove(ctx, "active-1")
	assertNotExist(t, err)

	r3, k3, err := ms.Remove(ctx, "active-3")
	if err != nil {
		t.Fatal(err)
	}
	if r3 != a3.ID {
		t.Fatal("Expected remove ID to match create ID")
	}
	if k3 != snapshot.KindActive {
		t.Fatal("Expected active kind, got %v", k3)
	}

	r2, k2, err := ms.Remove(ctx, "active-2")
	if err != nil {
		t.Fatal(err)
	}
	if r2 != a2.ID {
		t.Fatal("Expected remove ID to match create ID")
	}
	if k2 != snapshot.KindActive {
		t.Fatal("Expected active kind, got %v", k2)
	}

	r1, k1, err := ms.Remove(ctx, "committed-1")
	if err != nil {
		t.Fatal(err)
	}
	if r1 != commitID {
		t.Fatal("Expected remove ID to match commit ID")
	}
	if k1 != snapshot.KindCommitted {
		t.Fatal("Expected committed kind, got %v", k1)
	}
}

func testRemoveWithChildren(ctx context.Context, t *testing.T, ms storage.MetaStore) {
	if err := basePopulate(ctx, ms); err != nil {
		t.Fatalf("Populate failed: %+v", err)
	}
	_, _, err := ms.Remove(ctx, "committed-1")
	if err == nil {
		t.Fatalf("Expected removal of snapshot with children to error")
	}
	_, _, err = ms.Remove(ctx, "committed-1")
	if err == nil {
		t.Fatalf("Expected removal of snapshot with children to error")
	}
}

func testRemoveNotExist(ctx context.Context, t *testing.T, ms storage.MetaStore) {
	_, _, err := ms.Remove(ctx, "does-not-exist")
	assertNotExist(t, err)
}
