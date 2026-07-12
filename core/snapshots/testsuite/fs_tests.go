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
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/internal/randutil"
	"github.com/containerd/errdefs"
)

// ---------------------------------------------------------------------------
// Basic lifecycle
// ---------------------------------------------------------------------------

// fsCheckSnapshotterBasic exercises the full lifecycle: create a layer, commit
// it, stack a second layer, and verify the stacked result matches the
// flattened result at each step.
func fsCheckSnapshotterBasic(ctx context.Context, t *testing.T, sn snapshots.Snapshotter, work string) {
	initial := []fsChange{
		fsCreateFile("/foo", []byte("foo\n"), 0777),
		fsMkdir("/a", 0755),
		fsMkdir("/a/b", 0755),
		fsMkdir("/a/b/c", 0755),
	}
	diff := []fsChange{
		fsCreateFile("/bar", []byte("bar\n"), 0777),
		fsCreateFile("/foo", []byte("bar\n"), 0777), // overwrite foo
		fsRemove("/a/b"),
	}

	if err := fsCheckSnapshots(ctx, sn, work, initial, diff); err != nil {
		t.Fatalf("check snapshots: %+v", err)
	}

	// Verify the lifecycle/stat/remove semantics on a concrete chain.
	c1, err := fsCreateLayer(ctx, sn, "", work, initial)
	if err != nil {
		t.Fatal(err)
	}
	si, err := sn.Stat(ctx, c1)
	if err != nil {
		t.Fatal(err)
	}
	assert.Empty(t, si.Parent)
	assert.Equal(t, snapshots.KindCommitted, si.Kind)

	c2, err := fsCreateLayer(ctx, sn, c1, work, diff)
	if err != nil {
		t.Fatal(err)
	}
	si2, err := sn.Stat(ctx, c2)
	if err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, c1, si2.Parent)
	assert.Equal(t, snapshots.KindCommitted, si2.Kind)

	// Removing a parent with a child must fail.
	err = sn.Remove(ctx, c1)
	assert.NotNil(t, err)
	if err != nil {
		assert.Contains(t, err.Error(), "remove")
	}
	assert.Nil(t, sn.Remove(ctx, c2))
	assert.Nil(t, sn.Remove(ctx, c1))
}

// ---------------------------------------------------------------------------
// Stat tests
// ---------------------------------------------------------------------------

func fsCheckSnapshotterStatActive(ctx context.Context, t *testing.T, sn snapshots.Snapshotter, work string) {
	preparing := filepath.Join(work, "preparing")

	mounts, err := sn.Prepare(ctx, preparing, "", fsOpt)
	if err != nil {
		t.Fatal(err)
	}
	if len(mounts) < 1 {
		t.Fatal("expected mounts to have entries")
	}
	if err := applyLayer(mounts, []fsChange{fsCreateFile("/foo", []byte("foo\n"), 0777)}); err != nil {
		_ = sn.Remove(ctx, preparing)
		t.Fatal(err)
	}

	si, err := sn.Stat(ctx, preparing)
	if err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, si.Name, preparing)
	assert.Equal(t, snapshots.KindActive, si.Kind)
	assert.Equal(t, "", si.Parent)
}

func fsCheckSnapshotterStatCommitted(ctx context.Context, t *testing.T, sn snapshots.Snapshotter, work string) {
	committed, err := fsCreateLayer(ctx, sn, "", work, []fsChange{fsCreateFile("/foo", []byte("foo\n"), 0777)})
	if err != nil {
		t.Fatal(err)
	}
	si, err := sn.Stat(ctx, committed)
	if err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, si.Name, committed)
	assert.Equal(t, snapshots.KindCommitted, si.Kind)
	assert.Equal(t, "", si.Parent)
}

// ---------------------------------------------------------------------------
// Transitivity
// ---------------------------------------------------------------------------

func fsCheckSnapshotterTransitivity(ctx context.Context, t *testing.T, sn snapshots.Snapshotter, work string) {
	snapA, err := fsCreateLayer(ctx, sn, "", work, []fsChange{fsCreateFile("/foo", []byte("foo\n"), 0777)})
	if err != nil {
		t.Fatal(err)
	}
	snapB, err := fsCreateLayer(ctx, sn, snapA, work, []fsChange{fsCreateFile("/foo", []byte("foo bar\n"), 0777)})
	if err != nil {
		t.Fatal(err)
	}

	siA, err := sn.Stat(ctx, snapA)
	if err != nil {
		t.Fatal(err)
	}
	siB, err := sn.Stat(ctx, snapB)
	if err != nil {
		t.Fatal(err)
	}
	siParentB, err := sn.Stat(ctx, siB.Parent)
	if err != nil {
		t.Fatal(err)
	}

	assert.Equal(t, "", siA.Parent)
	assert.Equal(t, snapA, siB.Parent)
	assert.Equal(t, "", siParentB.Parent)
}

// ---------------------------------------------------------------------------
// Duplicate Prepare/View keys
// ---------------------------------------------------------------------------

func fsCheckSnapshotterPrepareView(ctx context.Context, t *testing.T, sn snapshots.Snapshotter, work string) {
	snapA, err := fsCreateLayer(ctx, sn, "", work, nil)
	if err != nil {
		t.Fatal(err)
	}

	newLayer := filepath.Join(work, "newlayer")
	if _, err = sn.Prepare(ctx, newLayer, snapA, fsOpt); err != nil {
		t.Fatal(err)
	}
	_, err = sn.View(ctx, newLayer, snapA, fsOpt)
	assert.True(t, err != nil)

	prepLayer := filepath.Join(work, "prepLayer")
	if _, err = sn.Prepare(ctx, prepLayer, snapA, fsOpt); err != nil {
		t.Fatal(err)
	}
	_, err = sn.Prepare(ctx, prepLayer, snapA, fsOpt)
	assert.True(t, err != nil)

	viewLayer := filepath.Join(work, "viewLayer")
	if _, err = sn.View(ctx, viewLayer, snapA, fsOpt); err != nil {
		t.Fatal(err)
	}
	_, err = sn.View(ctx, viewLayer, snapA, fsOpt)
	assert.True(t, err != nil)
}

// ---------------------------------------------------------------------------
// Update
// ---------------------------------------------------------------------------

func fsCheckUpdate(ctx context.Context, t *testing.T, sn snapshots.Snapshotter, work string) {
	t1 := time.Now().UTC()
	if err := fsBaseTestSnapshots(ctx, sn, work); err != nil {
		t.Fatalf("Failed to create base snapshots: %v", err)
	}
	t2 := time.Now().UTC()

	testcases := []struct {
		name   string
		kind   snapshots.Kind
		parent string
	}{
		{name: "c1", kind: snapshots.KindCommitted},
		{name: "c2", kind: snapshots.KindCommitted, parent: "c1"},
		{name: "a1", kind: snapshots.KindActive, parent: "c2"},
		{name: "a2", kind: snapshots.KindActive},
		{name: "v1", kind: snapshots.KindView, parent: "c2"},
		{name: "v2", kind: snapshots.KindView},
	}

	for _, tc := range testcases {
		st, err := sn.Stat(ctx, tc.name)
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

		createdAt := st.Created
		rootTime := time.Now().UTC().Format(time.RFC3339)
		expectedLabels := map[string]string{
			"l1":                    "v1",
			"l2":                    "v2",
			"l3":                    "v3",
			"containerd.io/gc.root": rootTime,
		}
		st.Parent = "doesnotexist"
		st.Labels = expectedLabels
		u1 := time.Now().UTC()
		st, err = sn.Update(ctx, st)
		if err != nil {
			t.Fatalf("Failed to update %s: %v", tc.name, err)
		}
		u2 := time.Now().UTC()

		if st.Created != createdAt {
			t.Errorf("(%s) wrong created time after Update %s: expected %s", tc.name, st.Created, createdAt)
		}
		if st.Updated.Before(u1) || st.Updated.After(u2) {
			t.Errorf("(%s) wrong updated time %s: expected between %s and %s", tc.name, st.Updated, u1, u2)
		}
		if st.Kind != tc.kind {
			t.Errorf("(%s) unexpected kind %s after Update", tc.name, st.Kind)
		}
		if st.Parent != tc.parent {
			t.Errorf("(%s) unexpected parent %q after Update (should be immutable)", tc.name, st.Parent)
		}
		assertLabels(t, st.Labels, expectedLabels)

		expectedLabels2 := map[string]string{
			"l1":                    "updated",
			"l3":                    "v3",
			"containerd.io/gc.root": rootTime,
		}
		st.Labels = map[string]string{"l1": "updated", "l4": "v4"}
		st, err = sn.Update(ctx, st, "labels.l1", "labels.l2")
		if err != nil {
			t.Fatalf("Failed to update %s: %v", tc.name, err)
		}
		assertLabels(t, st.Labels, expectedLabels2)

		expectedLabels3 := map[string]string{
			"l4":                    "v4",
			"containerd.io/gc.root": rootTime,
		}
		st.Labels = expectedLabels3
		st, err = sn.Update(ctx, st, "labels")
		if err != nil {
			t.Fatalf("Failed to update %s: %v", tc.name, err)
		}
		assertLabels(t, st.Labels, expectedLabels3)

		st.Parent = "doesnotexist"
		st, err = sn.Update(ctx, st, "parent")
		if err == nil {
			t.Errorf("Expected error updating with immutable field path")
		} else if !errdefs.IsInvalidArgument(err) {
			t.Fatalf("Unexpected error updating %s: %+v", tc.name, err)
		}
	}
}

// fsBaseTestSnapshots creates the base snapshot set used by Update.
func fsBaseTestSnapshots(ctx context.Context, sn snapshots.Snapshotter, work string) error {
	if err := fsCommitNamed(ctx, sn, work, "c1-a", "c1", ""); err != nil {
		return err
	}
	if err := fsCommitNamed(ctx, sn, work, "c2-a", "c2", "c1"); err != nil {
		return err
	}
	if _, err := sn.Prepare(ctx, "a1", "c2", fsOpt); err != nil {
		return err
	}
	if _, err := sn.Prepare(ctx, "a2", "", fsOpt); err != nil {
		return err
	}
	if _, err := sn.View(ctx, "v1", "c2", fsOpt); err != nil {
		return err
	}
	if _, err := sn.View(ctx, "v2", "", fsOpt); err != nil {
		return err
	}
	return nil
}

// ---------------------------------------------------------------------------
// Remove
// ---------------------------------------------------------------------------

func fsCheckRemove(ctx context.Context, t *testing.T, sn snapshots.Snapshotter, work string) {
	if err := fsCommitNamed(ctx, sn, work, "committed-a", "committed-1", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := sn.Prepare(ctx, "reuse-1", "committed-1", fsOpt); err != nil {
		t.Fatal(err)
	}
	if err := sn.Remove(ctx, "reuse-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := sn.View(ctx, "reuse-1", "committed-1", fsOpt); err != nil {
		t.Fatal(err)
	}
	if err := sn.Remove(ctx, "reuse-1"); err != nil {
		t.Fatal(err)
	}
	// Prepare reuse-1, remove committed-1 while reuse-1 is active (no parent),
	// then commit reuse-1 as committed-1. The Prepare/apply/Commit is split
	// because the removal of committed-1 happens between them.
	mounts, err := sn.Prepare(ctx, "reuse-1", "", fsOpt)
	if err != nil {
		t.Fatal(err)
	}
	if err := applyLayer(mounts, nil); err != nil {
		_ = sn.Remove(ctx, "reuse-1")
		t.Fatal(err)
	}
	if err := sn.Remove(ctx, "committed-1"); err != nil {
		t.Fatal(err)
	}
	if err := sn.Commit(ctx, "committed-1", "reuse-1", fsOpt); err != nil {
		t.Fatal(err)
	}
}

// ---------------------------------------------------------------------------
// Walk
// ---------------------------------------------------------------------------

func fsCheckWalk(ctx context.Context, t *testing.T, sn snapshots.Snapshotter, work string) {
	walkOpt := snapshots.WithLabels(map[string]string{
		"containerd.io/gc.root": "check-walk",
	})
	walkLabel1Opt := snapshots.WithLabels(map[string]string{
		"l":                     "1",
		"containerd.io/gc.root": "check-walk",
	})

	if _, err := sn.Prepare(ctx, "a-np", "", walkOpt); err != nil {
		t.Fatal(err)
	}
	if err := fsCommitNamed(ctx, sn, work, "p-tmp", "p", "", walkOpt); err != nil {
		t.Fatal(err)
	}
	if _, err := sn.Prepare(ctx, "a", "p", walkOpt); err != nil {
		t.Fatal(err)
	}
	if _, err := sn.View(ctx, "v", "p", walkOpt); err != nil {
		t.Fatal(err)
	}
	if err := fsCommitNamed(ctx, sn, work, "p-wl-tmp", "p-wl", "", walkLabel1Opt); err != nil {
		t.Fatal(err)
	}
	if _, err := sn.Prepare(ctx, "a-wl", "p-wl", snapshots.WithLabels(map[string]string{
		"l":                     "2",
		"containerd.io/gc.root": "check-walk",
	})); err != nil {
		t.Fatal(err)
	}
	if _, err := sn.View(ctx, "v-wl", "p-wl", snapshots.WithLabels(map[string]string{
		"l":                     "3",
		"containerd.io/gc.root": "check-walk",
	})); err != nil {
		t.Fatal(err)
	}
	if _, err := sn.Prepare(ctx, "a-np-wl", "", snapshots.WithLabels(map[string]string{
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
			filters: []string{`labels."containerd.io/gc.root"==check-walk`},
		},
		{
			matches: []string{"a-np", "a", "a-wl", "a-np-wl"},
			filters: []string{`kind==active,labels."containerd.io/gc.root"==check-walk`},
		},
		{
			matches: []string{"v", "v-wl"},
			filters: []string{`kind==view,labels."containerd.io/gc.root"==check-walk`},
		},
		{
			matches: []string{"p", "p-wl"},
			filters: []string{`kind==committed,labels."containerd.io/gc.root"==check-walk`},
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
			filters: []string{`parent!="",labels."containerd.io/gc.root"==check-walk`},
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
		err := sn.Walk(ctx, func(ctx context.Context, si snapshots.Info) error {
			actual = append(actual, si.Name)
			return nil
		}, tc.filters...)
		if err != nil {
			t.Fatal(err)
		}

		sort.Strings(tc.matches)
		sort.Strings(actual)
		if len(actual) != len(tc.matches) {
			t.Errorf("[%d] size mismatch:\n got %#v\nwant %#v", i, actual, tc.matches)
			continue
		}
		for j := range actual {
			if actual[j] != tc.matches[j] {
				t.Errorf("[%d] @%d:\n got %#v\nwant %#v", i, j, actual, tc.matches)
				break
			}
		}
	}
}

// ---------------------------------------------------------------------------
// View read-only check
// ---------------------------------------------------------------------------

func fsCheckSnapshotterViewReadonly(ctx context.Context, t *testing.T, sn snapshots.Snapshotter, work string) {
	committed := filepath.Join(work, "committed")
	if err := fsCommitNamed(ctx, sn, work, filepath.Join(work, "preparing"), committed, ""); err != nil {
		t.Fatal(err)
	}

	view := filepath.Join(work, "view")
	m, err := sn.View(ctx, view, committed, fsOpt)
	if err != nil {
		t.Fatal(err)
	}
	defer sn.Remove(ctx, view)
	defer sn.Remove(ctx, committed)

	hasRO := false
	for _, mnt := range m {
		for _, opt := range mnt.Options {
			if opt == "ro" {
				hasRO = true
			}
		}
	}
	if !hasRO {
		t.Logf("View mounts: %+v", m)
		t.Error("View mounts should have 'ro' option")
	}

	// The fs.FS interface is read-only by contract; confirm the view opens.
	v, closeV, err := fsMountsView(m)
	if err != nil {
		t.Fatalf("FSMounts: %+v", err)
	}
	defer closeV()
	root, err := v.Open(".")
	if err != nil {
		t.Fatalf("Open root via view: %+v", err)
	}
	root.Close()
}

// ---------------------------------------------------------------------------
// StatInWalk
// ---------------------------------------------------------------------------

func fsCheckStatInWalk(ctx context.Context, t *testing.T, sn snapshots.Snapshotter, work string) {
	prefix := "fs-stats-in-walk-"
	if err := fsCreateNamedSnapshots(ctx, sn, work, prefix); err != nil {
		t.Fatal(err)
	}

	err := sn.Walk(ctx, func(ctx context.Context, si snapshots.Info) error {
		if !strings.HasPrefix(si.Name, prefix) {
			return nil
		}
		si2, err := sn.Stat(ctx, si.Name)
		if err != nil {
			return err
		}
		return checkInfo(si, si2)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func fsCreateNamedSnapshots(ctx context.Context, sn snapshots.Snapshotter, work, ns string) error {
	c1 := fmt.Sprintf("%sc1", ns)
	c2 := fmt.Sprintf("%sc2", ns)
	if err := fsCommitNamed(ctx, sn, work, c1+"-a", c1, ""); err != nil {
		return err
	}
	if err := fsCommitNamed(ctx, sn, work, c2+"-a", c2, c1); err != nil {
		return err
	}
	if _, err := sn.Prepare(ctx, fmt.Sprintf("%sa1", ns), c2, fsOpt); err != nil {
		return err
	}
	if _, err := sn.View(ctx, fmt.Sprintf("%sv1", ns), c2, fsOpt); err != nil {
		return err
	}
	return nil
}

// ---------------------------------------------------------------------------
// CloseTwice
// ---------------------------------------------------------------------------

func fsCloseTwice(ctx context.Context, t *testing.T, sn snapshots.Snapshotter, work string) {
	n := fmt.Sprintf("fsCloseTwice-%d", randutil.Int())
	if err := fsCommitNamed(ctx, sn, work, n+"-prepare", n, ""); err != nil {
		t.Fatal(err)
	}
	if err := sn.Remove(ctx, n); err != nil {
		t.Fatal(err)
	}
	if err := sn.Close(); err != nil {
		t.Fatalf("First close failed: %+v", err)
	}
	if err := sn.Close(); err != nil {
		t.Fatalf("Second close failed: %+v", err)
	}
}

// ---------------------------------------------------------------------------
// RootPermission
// ---------------------------------------------------------------------------

func fsCheckRootPermission(ctx context.Context, t *testing.T, sn snapshots.Snapshotter, work string) {
	if runtime.GOOS == "windows" {
		t.Skip("Filesystem permissions are not supported on Windows")
	}

	preparing := filepath.Join(work, "preparing")
	mounts, err := sn.Prepare(ctx, preparing, "", fsOpt)
	if err != nil {
		t.Fatal(err)
	}
	defer sn.Remove(ctx, preparing)

	v, closeV, err := fsMountsView(mounts)
	if err != nil {
		t.Fatalf("FSMounts: %+v", err)
	}
	defer closeV()

	f, err := v.Open(".")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0755 {
		t.Fatalf("expected root perm 0755, got 0%o", perm)
	}
}

// ---------------------------------------------------------------------------
// RemoveIntermediateSnapshot
// ---------------------------------------------------------------------------

func fsCheckRemoveIntermediateSnapshot(ctx context.Context, t *testing.T, sn snapshots.Snapshotter, work string) {
	base, err := fsCreateLayer(ctx, sn, "", work, nil)
	if err != nil {
		t.Fatal(err)
	}
	inter, err := fsCreateLayer(ctx, sn, base, work, nil)
	if err != nil {
		t.Fatal(err)
	}

	topLayer := filepath.Join(work, "toplayer")
	if _, err = sn.Prepare(ctx, topLayer, inter, fsOpt); err != nil {
		t.Fatal(err)
	}

	if err = sn.Remove(ctx, inter); err == nil {
		t.Fatal("intermediate layer removal should fail")
	}

	if err = sn.Remove(ctx, topLayer); err != nil {
		t.Fatal(err)
	}
	if err = sn.Remove(ctx, inter); err != nil {
		t.Fatal(err)
	}
	if err = sn.Remove(ctx, base); err != nil {
		t.Fatal(err)
	}
}

// ---------------------------------------------------------------------------
// Rename (snapshotter-specific behaviour)
// ---------------------------------------------------------------------------

func fsCheckRename(name string) func(ctx context.Context, t *testing.T, sn snapshots.Snapshotter, work string) {
	return func(ctx context.Context, t *testing.T, sn snapshots.Snapshotter, work string) {
		l1 := []fsChange{
			fsMkdir("/dir1", 0700),
			fsMkdir("/somefiles", 0700),
			fsCreateFile("/somefiles/f1", []byte("was here first!"), 0644),
			fsCreateFile("/somefiles/f2", []byte("nothing interesting"), 0644),
		}

		var l2 []fsChange
		// Renaming a lower-layer directory needs overlay redirect_dir support,
		// which the overlay-composed snapshotters lack; skip that one change.
		switch name {
		case "overlayfs", "fuse-overlayfs", "erofs":
		default:
			l2 = append(l2, fsRename("/dir1", "/dir2"))
		}
		l2 = append(l2,
			fsCreateFile("/somefiles/f1-overwrite", []byte("new content 1"), 0644),
			fsRename("/somefiles/f1-overwrite", "/somefiles/f1"),
			fsRename("/somefiles/f2", "/somefiles/f3"),
		)

		if err := fsCheckSnapshots(ctx, sn, work, l1, l2); err != nil {
			t.Fatalf("check snapshots: %+v", err)
		}
	}
}

// ---------------------------------------------------------------------------
// 128 Layers
// ---------------------------------------------------------------------------

func fsCheck128Layers(name string) func(ctx context.Context, t *testing.T, sn snapshots.Snapshotter, work string) {
	return func(ctx context.Context, t *testing.T, sn snapshots.Snapshotter, work string) {
		layers := [][]fsChange{{
			fsCreateFile("/bottom", []byte("way at the bottom\n"), 0777),
			fsCreateFile("/overwriteme", []byte("FIRST!\n"), 0777),
			fsMkdir("/addhere", 0755),
			fsMkdir("/onlyme", 0755),
			fsCreateFile("/onlyme/bottom", []byte("bye!\n"), 0777),
		}}
		for i := 1; i <= 127; i++ {
			layers = append(layers, []fsChange{
				fsCreateFile("/overwriteme", []byte(fmt.Sprintf("%d WAS HERE!\n", i)), 0777),
				fsCreateFile(fmt.Sprintf("/addhere/file-%d", i), []byte("same\n"), 0755),
				fsRemove("/onlyme"),
				fsMkdir("/onlyme", 0755),
				fsCreateFile(fmt.Sprintf("/onlyme/file-%d", i), []byte("only me!\n"), 0777),
			})
		}

		if err := fsCheckSnapshots(ctx, sn, work, layers...); err != nil {
			t.Fatalf("check snapshots: %+v", err)
		}
	}
}

// ---------------------------------------------------------------------------
// DeletedFilesInChildSnapshot
// ---------------------------------------------------------------------------

func fsCheckDeletedFilesInChildSnapshot(ctx context.Context, t *testing.T, sn snapshots.Snapshotter, work string) {
	l1 := []fsChange{
		fsCreateFile("/foo", []byte("foo\n"), 0777),
		fsCreateFile("/foobar", []byte("foobar\n"), 0777),
	}
	l2 := []fsChange{fsRemove("/foobar")}
	var l3 []fsChange

	if err := fsCheckSnapshots(ctx, sn, work, l1, l2, l3); err != nil {
		t.Fatalf("check snapshots: %+v", err)
	}
}

// ---------------------------------------------------------------------------
// MoveFileFromLowerLayer
// ---------------------------------------------------------------------------

func fsCheckFileFromLowerLayer(ctx context.Context, t *testing.T, sn snapshots.Snapshotter, work string) {
	l1 := []fsChange{
		fsMkdir("/dir1", 0700),
		fsCreateFile("/dir1/f1", []byte("Hello"), 0644),
		fsMkdir("/dir2", 0700),
		fsCreateFile("/dir2/f2", []byte("..."), 0644),
	}
	l2 := []fsChange{
		fsMkdir("/dir3", 0700),
		fsCreateFile("/dir3/f1", []byte("Hello"), 0644),
		fsRemove("/dir1"),
		fsLink("/dir2/f2", "/dir3/f2"),
		fsRemove("/dir2/f2"),
	}

	if err := fsCheckSnapshots(ctx, sn, work, l1, l2); err != nil {
		t.Fatalf("check snapshots: %+v", err)
	}
}
