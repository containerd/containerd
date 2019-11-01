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

package testsuite

import (
	"context"
	"fmt"
	"io/ioutil"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/log/logtest"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/pkg/testutil"
	"github.com/containerd/containerd/snapshots"
	"github.com/containerd/continuity/fs/fstest"
	"gotest.tools/assert"
	is "gotest.tools/assert/cmp"
)

// SnapshotterSuite runs a test suite on the snapshotter given a factory function.
func SnapshotterSuite(t *testing.T, name string, snapshotterFn func(ctx context.Context, root string) (snapshots.Snapshotter, func() error, error)) {
	restoreMask := clearMask()
	defer restoreMask()

	t.Run("Basic", makeTest(name, snapshotterFn, checkSnapshotterBasic))
	t.Run("StatActive", makeTest(name, snapshotterFn, checkSnapshotterStatActive))
	t.Run("StatComitted", makeTest(name, snapshotterFn, checkSnapshotterStatCommitted))
	t.Run("TransitivityTest", makeTest(name, snapshotterFn, checkSnapshotterTransitivity))
	t.Run("PreareViewFailingtest", makeTest(name, snapshotterFn, checkSnapshotterPrepareView))
	t.Run("Update", makeTest(name, snapshotterFn, checkUpdate))
	t.Run("Remove", makeTest(name, snapshotterFn, checkRemove))
	t.Run("Walk", makeTest(name, snapshotterFn, checkWalk))

	t.Run("LayerFileupdate", makeTest(name, snapshotterFn, checkLayerFileUpdate))
	t.Run("RemoveDirectoryInLowerLayer", makeTest(name, snapshotterFn, checkRemoveDirectoryInLowerLayer))
	t.Run("Chown", makeTest(name, snapshotterFn, checkChown))
	t.Run("DirectoryPermissionOnCommit", makeTest(name, snapshotterFn, checkDirectoryPermissionOnCommit))
	t.Run("RemoveIntermediateSnapshot", makeTest(name, snapshotterFn, checkRemoveIntermediateSnapshot))
	t.Run("DeletedFilesInChildSnapshot", makeTest(name, snapshotterFn, checkDeletedFilesInChildSnapshot))
	t.Run("MoveFileFromLowerLayer", makeTest(name, snapshotterFn, checkFileFromLowerLayer))
	t.Run("Rename", makeTest(name, snapshotterFn, checkRename))

	t.Run("ViewReadonly", makeTest(name, snapshotterFn, checkSnapshotterViewReadonly))

	t.Run("StatInWalk", makeTest(name, snapshotterFn, checkStatInWalk))
	t.Run("CloseTwice", makeTest(name, snapshotterFn, closeTwice))
	t.Run("RootPermission", makeTest(name, snapshotterFn, checkRootPermission))

	t.Run("128LayersMount", makeTest(name, snapshotterFn, check128LayersMount(name)))
}

func makeTest(name string, snapshotterFn func(ctx context.Context, root string) (snapshots.Snapshotter, func() error, error), fn func(ctx context.Context, t *testing.T, snapshotter snapshots.Snapshotter, work string)) func(t *testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := logtest.WithT(context.Background(), t)
		ctx = namespaces.WithNamespace(ctx, "testsuite")
		// Make two directories: a snapshotter root and a play area for the tests:
		//
		// 	/tmp
		// 		work/ -> passed to test functions
		// 		root/ -> passed to snapshotter
		//
		tmpDir, err := ioutil.TempDir("", "snapshot-suite-"+name+"-")
		if err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(tmpDir)

		root := filepath.Join(tmpDir, "root")
		if err := os.MkdirAll(root, 0777); err != nil {
			t.Fatal(err)
		}

		snapshotter, cleanup, err := snapshotterFn(ctx, root)
		if err != nil {
			t.Fatalf("Failed to initialize snapshotter: %+v", err)
		}
		defer func() {
			if cleanup != nil {
				if err := cleanup(); err != nil {
					t.Errorf("Cleanup failed: %v", err)
				}
			}
		}()

		work := filepath.Join(tmpDir, "work")
		if err := os.MkdirAll(work, 0777); err != nil {
			t.Fatal(err)
		}

		defer testutil.DumpDirOnFailure(t, tmpDir)
		fn(ctx, t, snapshotter, work)
	}
}

var opt = snapshots.WithLabels(map[string]string{
	"containerd.io/gc.root": time.Now().UTC().Format(time.RFC3339),
})

// checkSnapshotterBasic tests the basic workflow of a snapshot snapshotter.
func checkSnapshotterBasic(ctx context.Context, t *testing.T, snapshotter snapshots.Snapshotter, work string) {
	initialApplier := fstest.Apply(
		fstest.CreateFile("/foo", []byte("foo\n"), 0777),
		fstest.CreateDir("/a", 0755),
		fstest.CreateDir("/a/b", 0755),
		fstest.CreateDir("/a/b/c", 0755),
	)

	diffApplier := fstest.Apply(
		fstest.CreateFile("/bar", []byte("bar\n"), 0777),
		// also, change content of foo to bar
		fstest.CreateFile("/foo", []byte("bar\n"), 0777),
		fstest.RemoveAll("/a/b"),
	)

	preparing := filepath.Join(work, "preparing")
	if err := os.MkdirAll(preparing, 0777); err != nil {
		t.Fatalf("failure reason: %+v", err)
	}

	mounts, err := snapshotter.Prepare(ctx, preparing, "", opt)
	if err != nil {
		t.Fatalf("failure reason: %+v", err)
	}

	if len(mounts) < 1 {
		t.Fatal("expected mounts to have entries")
	}

	if err := mount.All(mounts, preparing); err != nil {
		t.Fatalf("failure reason: %+v", err)
	}

	if err := initialApplier.Apply(preparing); err != nil {
		t.Fatalf("failure reason: %+v", err)
	}
	// unmount before commit
	testutil.Unmount(t, preparing)

	committed := filepath.Join(work, "committed")
	if err := snapshotter.Commit(ctx, committed, preparing, opt); err != nil {
		t.Fatalf("failure reason: %+v", err)
	}

	si, err := snapshotter.Stat(ctx, committed)
	if err != nil {
		t.Fatalf("failure reason: %+v", err)
	}

	assert.Check(t, is.Equal("", si.Parent))
	assert.Check(t, is.Equal(snapshots.KindCommitted, si.Kind))

	_, err = snapshotter.Stat(ctx, preparing)
	if err == nil {
		t.Fatalf("%s should no longer be available after Commit", preparing)
	}

	next := filepath.Join(work, "nextlayer")
	if err := os.MkdirAll(next, 0777); err != nil {
		t.Fatalf("failure reason: %+v", err)
	}

	mounts, err = snapshotter.Prepare(ctx, next, committed, opt)
	if err != nil {
		t.Fatalf("failure reason: %+v", err)
	}
	if err := mount.All(mounts, next); err != nil {
		t.Fatalf("failure reason: %+v", err)
	}

	if err := fstest.CheckDirectoryEqualWithApplier(next, initialApplier); err != nil {
		t.Fatalf("failure reason: %+v", err)
	}

	if err := diffApplier.Apply(next); err != nil {
		t.Fatalf("failure reason: %+v", err)
	}
	// unmount before commit
	testutil.Unmount(t, next)

	ni, err := snapshotter.Stat(ctx, next)
	if err != nil {
		t.Fatal(err)
	}

	assert.Check(t, is.Equal(committed, ni.Parent))
	assert.Check(t, is.Equal(snapshots.KindActive, ni.Kind))

	nextCommitted := filepath.Join(work, "committed-next")
	if err := snapshotter.Commit(ctx, nextCommitted, next, opt); err != nil {
		t.Fatalf("failure reason: %+v", err)
	}

	si2, err := snapshotter.Stat(ctx, nextCommitted)
	if err != nil {
		t.Fatalf("failure reason: %+v", err)
	}

	assert.Check(t, is.Equal(committed, si2.Parent))
	assert.Check(t, is.Equal(snapshots.KindCommitted, si2.Kind))

	_, err = snapshotter.Stat(ctx, next)
	if err == nil {
		t.Fatalf("%s should no longer be available after Commit", next)
	}

	expected := map[string]snapshots.Info{
		si.Name:  si,
		si2.Name: si2,
	}
	walked := map[string]snapshots.Info{} // walk is not ordered
	assert.NilError(t, snapshotter.Walk(ctx, func(ctx context.Context, si snapshots.Info) error {
		walked[si.Name] = si
		return nil
	}))

	for ek, ev := range expected {
		av, ok := walked[ek]
		if !ok {
			t.Errorf("Missing stat for %v", ek)
			continue
		}
		assert.Check(t, is.DeepEqual(ev, av))
	}

	nextnext := filepath.Join(work, "nextnextlayer")
	if err := os.MkdirAll(nextnext, 0777); err != nil {
		t.Fatalf("failure reason: %+v", err)
	}

	mounts, err = snapshotter.View(ctx, nextnext, nextCommitted, opt)
	if err != nil {
		t.Fatalf("failure reason: %+v", err)
	}
	if err := mount.All(mounts, nextnext); err != nil {
		t.Fatalf("failure reason: %+v", err)
	}

	if err := fstest.CheckDirectoryEqualWithApplier(nextnext,
		fstest.Apply(initialApplier, diffApplier)); err != nil {
		testutil.Unmount(t, nextnext)
		t.Fatalf("failure reason: %+v", err)
	}

	testutil.Unmount(t, nextnext)
	assert.NilError(t, snapshotter.Remove(ctx, nextnext))
	assert.Assert(t, is.ErrorContains(snapshotter.Remove(ctx, committed), "remove"))
	assert.NilError(t, snapshotter.Remove(ctx, nextCommitted))
	assert.NilError(t, snapshotter.Remove(ctx, committed))
}

// Create a New Layer on top of base layer with Prepare, Stat on new layer, should return Active layer.
func checkSnapshotterStatActive(ctx context.Context, t *testing.T, snapshotter snapshots.Snapshotter, work string) {
	preparing := filepath.Join(work, "preparing")
	if err := os.MkdirAll(preparing, 0777); err != nil {
		t.Fatal(err)
	}

	mounts, err := snapshotter.Prepare(ctx, preparing, "", opt)
	if err != nil {
		t.Fatal(err)
	}

	if len(mounts) < 1 {
		t.Fatal("expected mounts to have entries")
	}

	if err = mount.All(mounts, preparing); err != nil {
		t.Fatal(err)
	}
	defer testutil.Unmount(t, preparing)

	if err = ioutil.WriteFile(filepath.Join(preparing, "foo"), []byte("foo\n"), 0777); err != nil {
		t.Fatal(err)
	}

	si, err := snapshotter.Stat(ctx, preparing)
	if err != nil {
		t.Fatal(err)
	}
	assert.Check(t, is.Equal(si.Name, preparing))
	assert.Check(t, is.Equal(snapshots.KindActive, si.Kind))
	assert.Check(t, is.Equal("", si.Parent))
}

// Commit a New Layer on top of base layer with Prepare & Commit , Stat on new layer, should return Committed layer.
func checkSnapshotterStatCommitted(ctx context.Context, t *testing.T, snapshotter snapshots.Snapshotter, work string) {
	preparing := filepath.Join(work, "preparing")
	if err := os.MkdirAll(preparing, 0777); err != nil {
		t.Fatal(err)
	}

	mounts, err := snapshotter.Prepare(ctx, preparing, "", opt)
	if err != nil {
		t.Fatal(err)
	}

	if len(mounts) < 1 {
		t.Fatal("expected mounts to have entries")
	}

	if err = mount.All(mounts, preparing); err != nil {
		t.Fatal(err)
	}
	defer testutil.Unmount(t, preparing)

	if err = ioutil.WriteFile(filepath.Join(preparing, "foo"), []byte("foo\n"), 0777); err != nil {
		t.Fatal(err)
	}

	committed := filepath.Join(work, "committed")
	if err = snapshotter.Commit(ctx, committed, preparing, opt); err != nil {
		t.Fatal(err)
	}

	si, err := snapshotter.Stat(ctx, committed)
	if err != nil {
		t.Fatal(err)
	}
	assert.Check(t, is.Equal(si.Name, committed))
	assert.Check(t, is.Equal(snapshots.KindCommitted, si.Kind))
	assert.Check(t, is.Equal("", si.Parent))

}

func snapshotterPrepareMount(ctx context.Context, snapshotter snapshots.Snapshotter, diffPathName string, parent string, work string) (string, error) {
	preparing := filepath.Join(work, diffPathName)
	if err := os.MkdirAll(preparing, 0777); err != nil {
		return "", err
	}

	mounts, err := snapshotter.Prepare(ctx, preparing, parent, opt)
	if err != nil {
		return "", err
	}

	if len(mounts) < 1 {
		return "", fmt.Errorf("expected mounts to have entries")
	}

	if err = mount.All(mounts, preparing); err != nil {
		return "", err
	}
	return preparing, nil
}

// Given A <- B <- C, B is the parent of C and A is a transitive parent of C (in this case, a "grandparent")
func checkSnapshotterTransitivity(ctx context.Context, t *testing.T, snapshotter snapshots.Snapshotter, work string) {
	preparing, err := snapshotterPrepareMount(ctx, snapshotter, "preparing", "", work)
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Unmount(t, preparing)

	if err = ioutil.WriteFile(filepath.Join(preparing, "foo"), []byte("foo\n"), 0777); err != nil {
		t.Fatal(err)
	}

	snapA := filepath.Join(work, "snapA")
	if err = snapshotter.Commit(ctx, snapA, preparing, opt); err != nil {
		t.Fatal(err)
	}

	next, err := snapshotterPrepareMount(ctx, snapshotter, "next", snapA, work)
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Unmount(t, next)

	if err = ioutil.WriteFile(filepath.Join(next, "foo"), []byte("foo bar\n"), 0777); err != nil {
		t.Fatal(err)
	}

	snapB := filepath.Join(work, "snapB")
	if err = snapshotter.Commit(ctx, snapB, next, opt); err != nil {
		t.Fatal(err)
	}

	siA, err := snapshotter.Stat(ctx, snapA)
	if err != nil {
		t.Fatal(err)
	}

	siB, err := snapshotter.Stat(ctx, snapB)
	if err != nil {
		t.Fatal(err)
	}

	siParentB, err := snapshotter.Stat(ctx, siB.Parent)
	if err != nil {
		t.Fatal(err)
	}

	// Test the transivity
	assert.Check(t, is.Equal("", siA.Parent))
	assert.Check(t, is.Equal(snapA, siB.Parent))
	assert.Check(t, is.Equal("", siParentB.Parent))

}

// Creating two layers with Prepare or View with same key must fail.
func checkSnapshotterPrepareView(ctx context.Context, t *testing.T, snapshotter snapshots.Snapshotter, work string) {
	preparing, err := snapshotterPrepareMount(ctx, snapshotter, "preparing", "", work)
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Unmount(t, preparing)

	snapA := filepath.Join(work, "snapA")
	if err = snapshotter.Commit(ctx, snapA, preparing, opt); err != nil {
		t.Fatal(err)
	}

	// Prepare & View with same key
	newLayer := filepath.Join(work, "newlayer")
	if err = os.MkdirAll(preparing, 0777); err != nil {
		t.Fatal(err)
	}

	// Prepare & View with same key
	_, err = snapshotter.Prepare(ctx, newLayer, snapA, opt)
	if err != nil {
		t.Fatal(err)
	}

	_, err = snapshotter.View(ctx, newLayer, snapA, opt)
	assert.Check(t, err != nil)

	// Two Prepare with same key
	prepLayer := filepath.Join(work, "prepLayer")
	if err = os.MkdirAll(preparing, 0777); err != nil {
		t.Fatal(err)
	}

	_, err = snapshotter.Prepare(ctx, prepLayer, snapA, opt)
	if err != nil {
		t.Fatal(err)
	}

	_, err = snapshotter.Prepare(ctx, prepLayer, snapA, opt)
	assert.Check(t, err != nil)

	// Two View with same key
	viewLayer := filepath.Join(work, "viewLayer")
	if err = os.MkdirAll(preparing, 0777); err != nil {
		t.Fatal(err)
	}

	_, err = snapshotter.View(ctx, viewLayer, snapA, opt)
	if err != nil {
		t.Fatal(err)
	}

	_, err = snapshotter.View(ctx, viewLayer, snapA, opt)
	assert.Check(t, err != nil)

}

// Deletion of files/folder of base layer in new layer, On Commit, those files should not be visible.
func checkDeletedFilesInChildSnapshot(ctx context.Context, t *testing.T, snapshotter snapshots.Snapshotter, work string) {

	l1Init := fstest.Apply(
		fstest.CreateFile("/foo", []byte("foo\n"), 0777),
		fstest.CreateFile("/foobar", []byte("foobar\n"), 0777),
	)
	l2Init := fstest.Apply(
		fstest.RemoveAll("/foobar"),
	)
	l3Init := fstest.Apply()

	if err := checkSnapshots(ctx, snapshotter, work, l1Init, l2Init, l3Init); err != nil {
		t.Fatalf("Check snapshots failed: %+v", err)
	}

}

//Create three layers. Deleting intermediate layer must fail.
func checkRemoveIntermediateSnapshot(ctx context.Context, t *testing.T, snapshotter snapshots.Snapshotter, work string) {

	base, err := snapshotterPrepareMount(ctx, snapshotter, "base", "", work)
	if err != nil {
		t.Fatal(err)
	}

	committedBase := filepath.Join(work, "committed-base")
	if err = snapshotter.Commit(ctx, committedBase, base, opt); err != nil {
		t.Fatal(err)
	}

	// Create intermediate layer
	intermediate := filepath.Join(work, "intermediate")
	if _, err = snapshotter.Prepare(ctx, intermediate, committedBase, opt); err != nil {
		t.Fatal(err)
	}

	committedInter := filepath.Join(work, "committed-inter")
	if err = snapshotter.Commit(ctx, committedInter, intermediate, opt); err != nil {
		t.Fatal(err)
	}

	// Create top layer
	topLayer := filepath.Join(work, "toplayer")
	if _, err = snapshotter.Prepare(ctx, topLayer, committedInter, opt); err != nil {
		t.Fatal(err)
	}

	// Deletion of intermediate layer must fail.
	err = snapshotter.Remove(ctx, committedInter)
	if err == nil {
		t.Fatal("intermediate layer removal should fail.")
	}

	//Removal from toplayer to base should not fail.
	err = snapshotter.Remove(ctx, topLayer)
	if err != nil {
		t.Fatal(err)
	}
	err = snapshotter.Remove(ctx, committedInter)
	if err != nil {
		t.Fatal(err)
	}
	testutil.Unmount(t, base)
	err = snapshotter.Remove(ctx, committedBase)
	if err != nil {
		t.Fatal(err)
	}
}

// baseTestSnapshots creates a base set of snapshots for tests, each snapshot is empty
// Tests snapshots:
//  c1 - committed snapshot, no parent
//  c2 - committed snapshot, c1 is parent
//  a1 - active snapshot, c2 is parent
//  a1 - active snapshot, no parent
//  v1 - view snapshot, v1 is parent
//  v2 - view snapshot, no parent
func baseTestSnapshots(ctx context.Context, snapshotter snapshots.Snapshotter) error {
	if _, err := snapshotter.Prepare(ctx, "c1-a", "", opt); err != nil {
		return err
	}
	if err := snapshotter.Commit(ctx, "c1", "c1-a", opt); err != nil {
		return err
	}
	if _, err := snapshotter.Prepare(ctx, "c2-a", "c1", opt); err != nil {
		return err
	}
	if err := snapshotter.Commit(ctx, "c2", "c2-a", opt); err != nil {
		return err
	}
	if _, err := snapshotter.Prepare(ctx, "a1", "c2", opt); err != nil {
		return err
	}
	if _, err := snapshotter.Prepare(ctx, "a2", "", opt); err != nil {
		return err
	}
	if _, err := snapshotter.View(ctx, "v1", "c2", opt); err != nil {
		return err
	}
	if _, err := snapshotter.View(ctx, "v2", "", opt); err != nil {
		return err
	}
	return nil
}

func checkUpdate(ctx context.Context, t *testing.T, snapshotter snapshots.Snapshotter, work string) {
	t1 := time.Now().UTC()
	if err := baseTestSnapshots(ctx, snapshotter); err != nil {
		t.Fatalf("Failed to create base snapshots: %v", err)
	}
	t2 := time.Now().UTC()
	testcases := []struct {
		name   string
		kind   snapshots.Kind
		parent string
	}{
		{
			name: "c1",
			kind: snapshots.KindCommitted,
		},
		{
			name:   "c2",
			kind:   snapshots.KindCommitted,
			parent: "c1",
		},
		{
			name:   "a1",
			kind:   snapshots.KindActive,
			parent: "c2",
		},
		{
			name: "a2",
			kind: snapshots.KindActive,
		},
		{
			name:   "v1",
			kind:   snapshots.KindView,
			parent: "c2",
		},
		{
			name: "v2",
			kind: snapshots.KindView,
		},
	}
	for _, tc := range testcases {
		st, err := snapshotter.Stat(ctx, tc.name)
		if err != nil {
			t.Fatalf("Failed to stat %s: %v", tc.name, err)
		}
		if st.Created.Before(t1) || st.Created.After(t2) {
			t.Errorf("(%s) wrong created time %s: expected between %s and %s", tc.name, st.Created, t1, t2)
			continue
		}
		if st.Created != st.Updated {
			t.Errorf("(%s) unexpected updated time %s: expected %s", tc.name, st.Updated, st.Created)
			continue
		}
		if st.Kind != tc.kind {
			t.Errorf("(%s) unexpected kind %s, expected %s", tc.name, st.Kind, tc.kind)
			continue
		}
		if st.Parent != tc.parent {
			t.Errorf("(%s) unexpected parent %q, expected %q", tc.name, st.Parent, tc.parent)
			continue
		}
		if st.Name != tc.name {
			t.Errorf("(%s) unexpected name %q, expected %q", tc.name, st.Name, tc.name)
			continue
		}

		createdAt := st.Created
		rootTime := time.Now().UTC().Format(time.RFC3339)
		expected := map[string]string{
			"l1": "v1",
			"l2": "v2",
			"l3": "v3",
			// Keep root label
			"containerd.io/gc.root": rootTime,
		}
		st.Parent = "doesnotexist"
		st.Labels = expected
		u1 := time.Now().UTC()
		st, err = snapshotter.Update(ctx, st)
		if err != nil {
			t.Fatalf("Failed to update %s: %v", tc.name, err)
		}
		u2 := time.Now().UTC()

		if st.Created != createdAt {
			t.Errorf("(%s) wrong created time %s: expected %s", tc.name, st.Created, createdAt)
			continue
		}
		if st.Updated.Before(u1) || st.Updated.After(u2) {
			t.Errorf("(%s) wrong updated time %s: expected between %s and %s", tc.name, st.Updated, u1, u2)
			continue
		}
		if st.Kind != tc.kind {
			t.Errorf("(%s) unexpected kind %s, expected %s", tc.name, st.Kind, tc.kind)
			continue
		}
		if st.Parent != tc.parent {
			t.Errorf("(%s) unexpected parent %q, expected %q", tc.name, st.Parent, tc.parent)
			continue
		}
		if st.Name != tc.name {
			t.Errorf("(%s) unexpected name %q, expected %q", tc.name, st.Name, tc.name)
			continue
		}
		assertLabels(t, st.Labels, expected)

		expected = map[string]string{
			"l1": "updated",
			"l3": "v3",

			"containerd.io/gc.root": rootTime,
		}
		st.Labels = map[string]string{
			"l1": "updated",
			"l4": "v4",
		}
		st, err = snapshotter.Update(ctx, st, "labels.l1", "labels.l2")
		if err != nil {
			t.Fatalf("Failed to update %s: %v", tc.name, err)
		}
		assertLabels(t, st.Labels, expected)

		expected = map[string]string{
			"l4": "v4",

			"containerd.io/gc.root": rootTime,
		}
		st.Labels = expected
		st, err = snapshotter.Update(ctx, st, "labels")
		if err != nil {
			t.Fatalf("Failed to update %s: %v", tc.name, err)
		}
		assertLabels(t, st.Labels, expected)

		// Test failure received when providing immutable field path
		st.Parent = "doesnotexist"
		st, err = snapshotter.Update(ctx, st, "parent")
		if err == nil {
			t.Errorf("Expected error updating with immutable field path")
		} else if !errdefs.IsInvalidArgument(err) {
			t.Fatalf("Unexpected error updating %s: %+v", tc.name, err)
		}
	}
}

func assertLabels(t *testing.T, actual, expected map[string]string) {
	if len(actual) != len(expected) {
		t.Fatalf("Label size mismatch: %d vs %d\n\tActual: %#v\n\tExpected: %#v", len(actual), len(expected), actual, expected)
	}
	for k, v := range expected {
		if a := actual[k]; v != a {
			t.Errorf("Wrong label value for %s, got %q, expected %q", k, a, v)
		}
	}
	if t.Failed() {
		t.FailNow()
	}
}

func checkRemove(ctx context.Context, t *testing.T, snapshotter snapshots.Snapshotter, work string) {
	if _, err := snapshotter.Prepare(ctx, "committed-a", "", opt); err != nil {
		t.Fatal(err)
	}
	if err := snapshotter.Commit(ctx, "committed-1", "committed-a", opt); err != nil {
		t.Fatal(err)
	}
	if _, err := snapshotter.Prepare(ctx, "reuse-1", "committed-1", opt); err != nil {
		t.Fatal(err)
	}
	if err := snapshotter.Remove(ctx, "reuse-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := snapshotter.View(ctx, "reuse-1", "committed-1", opt); err != nil {
		t.Fatal(err)
	}
	if err := snapshotter.Remove(ctx, "reuse-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := snapshotter.Prepare(ctx, "reuse-1", "", opt); err != nil {
		t.Fatal(err)
	}
	if err := snapshotter.Remove(ctx, "committed-1"); err != nil {
		t.Fatal(err)
	}
	if err := snapshotter.Commit(ctx, "committed-1", "reuse-1", opt); err != nil {
		t.Fatal(err)
	}
}

// checkSnapshotterViewReadonly ensures a KindView snapshot to be mounted as a read-only filesystem.
// This function is called only when WithTestViewReadonly is true.
func checkSnapshotterViewReadonly(ctx context.Context, t *testing.T, snapshotter snapshots.Snapshotter, work string) {
	preparing := filepath.Join(work, "preparing")
	if _, err := snapshotter.Prepare(ctx, preparing, "", opt); err != nil {
		t.Fatal(err)
	}
	committed := filepath.Join(work, "committed")
	if err := snapshotter.Commit(ctx, committed, preparing, opt); err != nil {
		t.Fatal(err)
	}
	view := filepath.Join(work, "view")
	m, err := snapshotter.View(ctx, view, committed, opt)
	if err != nil {
		t.Fatal(err)
	}
	viewMountPoint := filepath.Join(work, "view-mount")
	if err := os.MkdirAll(viewMountPoint, 0777); err != nil {
		t.Fatal(err)
	}

	// Just checking the option string of m is not enough, we need to test real mount. (#1368)
	if err := mount.All(m, viewMountPoint); err != nil {
		t.Fatal(err)
	}

	testfile := filepath.Join(viewMountPoint, "testfile")
	if err := ioutil.WriteFile(testfile, []byte("testcontent"), 0777); err != nil {
		t.Logf("write to %q failed with %v (EROFS is expected but can be other error code)", testfile, err)
	} else {
		t.Fatalf("write to %q should fail (EROFS) but did not fail", testfile)
	}
	testutil.Unmount(t, viewMountPoint)
	assert.NilError(t, snapshotter.Remove(ctx, view))
	assert.NilError(t, snapshotter.Remove(ctx, committed))
}

// Move files from base layer to new location in intermediate layer.
// Verify if the file at source is deleted and copied to new location.
func checkFileFromLowerLayer(ctx context.Context, t *testing.T, snapshotter snapshots.Snapshotter, work string) {
	l1Init := fstest.Apply(
		fstest.CreateDir("/dir1", 0700),
		fstest.CreateFile("/dir1/f1", []byte("Hello"), 0644),
		fstest.CreateDir("dir2", 0700),
		fstest.CreateFile("dir2/f2", []byte("..."), 0644),
	)
	l2Init := fstest.Apply(
		fstest.CreateDir("/dir3", 0700),
		fstest.CreateFile("/dir3/f1", []byte("Hello"), 0644),
		fstest.RemoveAll("/dir1"),
		fstest.Link("dir2/f2", "dir3/f2"),
		fstest.RemoveAll("dir2/f2"),
	)

	if err := checkSnapshots(ctx, snapshotter, work, l1Init, l2Init); err != nil {
		t.Fatalf("Check snapshots failed: %+v", err)
	}
}

func closeTwice(ctx context.Context, t *testing.T, snapshotter snapshots.Snapshotter, work string) {
	n := fmt.Sprintf("closeTwice-%d", rand.Int())
	prepare := fmt.Sprintf("%s-prepare", n)

	// do some dummy ops to modify the snapshotter internal state
	if _, err := snapshotter.Prepare(ctx, prepare, "", opt); err != nil {
		t.Fatal(err)
	}
	if err := snapshotter.Commit(ctx, n, prepare, opt); err != nil {
		t.Fatal(err)
	}
	if err := snapshotter.Remove(ctx, n); err != nil {
		t.Fatal(err)
	}
	if err := snapshotter.Close(); err != nil {
		t.Fatalf("The first close failed: %+v", err)
	}
	if err := snapshotter.Close(); err != nil {
		t.Fatalf("The second close failed: %+v", err)
	}
}

func checkRootPermission(ctx context.Context, t *testing.T, snapshotter snapshots.Snapshotter, work string) {
	preparing, err := snapshotterPrepareMount(ctx, snapshotter, "preparing", "", work)
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Unmount(t, preparing)
	st, err := os.Stat(preparing)
	if err != nil {
		t.Fatal(err)
	}
	if mode := st.Mode() & 0777; mode != 0755 {
		t.Fatalf("expected 0755, got 0%o", mode)
	}
}

func check128LayersMount(name string) func(ctx context.Context, t *testing.T, snapshotter snapshots.Snapshotter, work string) {
	return func(ctx context.Context, t *testing.T, snapshotter snapshots.Snapshotter, work string) {
		if name == "Aufs" {
			t.Skip("aufs tests have issues with whiteouts here on some CI kernels")
		}
		lowestApply := fstest.Apply(
			fstest.CreateFile("/bottom", []byte("way at the bottom\n"), 0777),
			fstest.CreateFile("/overwriteme", []byte("FIRST!\n"), 0777),
			fstest.CreateDir("/ADDHERE", 0755),
			fstest.CreateDir("/ONLYME", 0755),
			fstest.CreateFile("/ONLYME/bottom", []byte("bye!\n"), 0777),
		)

		appliers := []fstest.Applier{lowestApply}
		for i := 1; i <= 127; i++ {
			appliers = append(appliers, fstest.Apply(
				fstest.CreateFile("/overwriteme", []byte(fmt.Sprintf("%d WAS HERE!\n", i)), 0777),
				fstest.CreateFile(fmt.Sprintf("/ADDHERE/file-%d", i), []byte("same\n"), 0755),
				fstest.RemoveAll("/ONLYME"),
				fstest.CreateDir("/ONLYME", 0755),
				fstest.CreateFile(fmt.Sprintf("/ONLYME/file-%d", i), []byte("only me!\n"), 0777),
			))
		}

		flat := filepath.Join(work, "flat")
		if err := os.MkdirAll(flat, 0777); err != nil {
			t.Fatalf("failed to create flat dir(%s): %+v", flat, err)
		}

		// NOTE: add gc labels to avoid snapshots get removed by gc...
		parent := ""
		for i, applier := range appliers {
			preparing := filepath.Join(work, fmt.Sprintf("prepare-layer-%d", i))
			if err := os.MkdirAll(preparing, 0777); err != nil {
				t.Fatalf("[layer %d] failed to create preparing dir(%s): %+v", i, preparing, err)
			}

			mounts, err := snapshotter.Prepare(ctx, preparing, parent, opt)
			if err != nil {
				t.Fatalf("[layer %d] failed to get mount info: %+v", i, err)
			}

			if err := mount.All(mounts, preparing); err != nil {
				t.Fatalf("[layer %d] failed to mount on the target(%s): %+v", i, preparing, err)
			}

			if err := fstest.CheckDirectoryEqual(preparing, flat); err != nil {
				testutil.Unmount(t, preparing)
				t.Fatalf("[layer %d] preparing doesn't equal to flat before apply: %+v", i, err)
			}

			if err := applier.Apply(flat); err != nil {
				testutil.Unmount(t, preparing)
				t.Fatalf("[layer %d] failed to apply on flat dir: %+v", i, err)
			}

			if err = applier.Apply(preparing); err != nil {
				testutil.Unmount(t, preparing)
				t.Fatalf("[layer %d] failed to apply on preparing dir: %+v", i, err)
			}

			if err := fstest.CheckDirectoryEqual(preparing, flat); err != nil {
				testutil.Unmount(t, preparing)
				t.Fatalf("[layer %d] preparing doesn't equal to flat after apply: %+v", i, err)
			}

			testutil.Unmount(t, preparing)

			parent = filepath.Join(work, fmt.Sprintf("committed-%d", i))
			if err := snapshotter.Commit(ctx, parent, preparing, opt); err != nil {
				t.Fatalf("[layer %d] failed to commit the preparing: %+v", i, err)
			}

		}

		view := filepath.Join(work, "fullview")
		if err := os.MkdirAll(view, 0777); err != nil {
			t.Fatalf("failed to create fullview dir(%s): %+v", view, err)
		}

		mounts, err := snapshotter.View(ctx, view, parent, opt)
		if err != nil {
			t.Fatalf("failed to get view's mount info: %+v", err)
		}

		if err := mount.All(mounts, view); err != nil {
			t.Fatalf("failed to mount on the target(%s): %+v", view, err)
		}
		defer testutil.Unmount(t, view)

		if err := fstest.CheckDirectoryEqual(view, flat); err != nil {
			t.Fatalf("fullview should equal to flat: %+v", err)
		}
	}
}

func checkWalk(ctx context.Context, t *testing.T, snapshotter snapshots.Snapshotter, work string) {
	opt := snapshots.WithLabels(map[string]string{
		"containerd.io/gc.root": "check-walk",
	})

	// No parent active
	if _, err := snapshotter.Prepare(ctx, "a-np", "", opt); err != nil {
		t.Fatal(err)
	}

	// Base parent
	if _, err := snapshotter.Prepare(ctx, "p-tmp", "", opt); err != nil {
		t.Fatal(err)
	}
	if err := snapshotter.Commit(ctx, "p", "p-tmp", opt); err != nil {
		t.Fatal(err)
	}

	// Active
	if _, err := snapshotter.Prepare(ctx, "a", "p", opt); err != nil {
		t.Fatal(err)
	}

	// View
	if _, err := snapshotter.View(ctx, "v", "p", opt); err != nil {
		t.Fatal(err)
	}

	// Base parent with label=1
	if _, err := snapshotter.Prepare(ctx, "p-wl-tmp", "", opt); err != nil {
		t.Fatal(err)
	}
	if err := snapshotter.Commit(ctx, "p-wl", "p-wl-tmp", snapshots.WithLabels(map[string]string{
		"l":                     "1",
		"containerd.io/gc.root": "check-walk",
	})); err != nil {
		t.Fatal(err)
	}

	// active with label=2
	if _, err := snapshotter.Prepare(ctx, "a-wl", "p-wl", snapshots.WithLabels(map[string]string{
		"l":                     "2",
		"containerd.io/gc.root": "check-walk",
	})); err != nil {
		t.Fatal(err)
	}

	// view with label=3
	if _, err := snapshotter.View(ctx, "v-wl", "p-wl", snapshots.WithLabels(map[string]string{
		"l":                     "3",
		"containerd.io/gc.root": "check-walk",
	})); err != nil {
		t.Fatal(err)
	}

	// no parent active with label=2
	if _, err := snapshotter.Prepare(ctx, "a-np-wl", "", snapshots.WithLabels(map[string]string{
		"l":                     "2",
		"containerd.io/gc.root": "check-walk",
	})); err != nil {
		t.Fatal(err)
	}

	for i, tc := range []struct {
		matches []string
		filters []string
	}{
		{
			matches: []string{"a-np", "p", "a", "v", "p-wl", "a-wl", "v-wl", "a-np-wl"},
			filters: []string{"labels.\"containerd.io/gc.root\"==check-walk"},
		},
		{
			matches: []string{"a-np", "a", "a-wl", "a-np-wl"},
			filters: []string{"kind==active,labels.\"containerd.io/gc.root\"==check-walk"},
		},
		{
			matches: []string{"v", "v-wl"},
			filters: []string{"kind==view,labels.\"containerd.io/gc.root\"==check-walk"},
		},
		{
			matches: []string{"p", "p-wl"},
			filters: []string{"kind==committed,labels.\"containerd.io/gc.root\"==check-walk"},
		},
		{
			matches: []string{"p", "a-np-wl"},
			filters: []string{"name==p", "name==a-np-wl"},
		},
		{
			matches: []string{"a-wl"},
			filters: []string{"name==a-wl,labels.l"},
		},
		{
			matches: []string{"a", "v"},
			filters: []string{"parent==p"},
		},
		{
			matches: []string{"a", "v", "a-wl", "v-wl"},
			filters: []string{"parent!=\"\",labels.\"containerd.io/gc.root\"==check-walk"},
		},
		{
			matches: []string{"p-wl", "a-wl", "v-wl", "a-np-wl"},
			filters: []string{"labels.l"},
		},
		{
			matches: []string{"a-wl", "a-np-wl"},
			filters: []string{"labels.l==2"},
		},
	} {
		actual := []string{}
		err := snapshotter.Walk(ctx, func(ctx context.Context, si snapshots.Info) error {
			actual = append(actual, si.Name)
			return nil
		}, tc.filters...)
		if err != nil {
			t.Fatal(err)
		}

		sort.Strings(tc.matches)
		sort.Strings(actual)
		if len(actual) != len(tc.matches) {
			t.Errorf("[%d] Unexpected result (size):\nActual:\n\t%#v\nExpected:\n\t%#v", i, actual, tc.matches)
			continue
		}
		for j := range actual {
			if actual[j] != tc.matches[j] {
				t.Errorf("[%d] Unexpected result @%d:\nActual:\n\t%#vExpected:\n\t%#v", i, j, actual, tc.matches)
				break
			}
		}
	}
}
