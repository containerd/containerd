package testsuite

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/containerd/containerd/fs/fstest"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/snapshot"
	"github.com/containerd/containerd/testutil"
	"github.com/stretchr/testify/assert"
)

// SnapshotterSuite runs a test suite on the snapshotter given a factory function.
func SnapshotterSuite(t *testing.T, name string, snapshotterFn func(ctx context.Context, root string) (snapshot.Snapshotter, func(), error)) {
	t.Run("Basic", makeTest(t, name, snapshotterFn, checkSnapshotterBasic))
	t.Run("StatActive", makeTest(t, name, snapshotterFn, checkSnapshotterStatActive))
	t.Run("StatComitted", makeTest(t, name, snapshotterFn, checkSnapshotterStatCommitted))
	t.Run("TransitivityTest", makeTest(t, name, snapshotterFn, checkSnapshotterTransitivity))
	t.Run("PreareViewFailingtest", makeTest(t, name, snapshotterFn, checkSnapshotterPrepareView))
}

func makeTest(t *testing.T, name string, snapshotterFn func(ctx context.Context, root string) (snapshot.Snapshotter, func(), error), fn func(ctx context.Context, t *testing.T, snapshotter snapshot.Snapshotter, work string)) func(t *testing.T) {
	return func(t *testing.T) {
		ctx := context.Background()
		oldumask := syscall.Umask(0)
		defer syscall.Umask(oldumask)
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
			t.Fatal(err)
		}
		defer cleanup()

		work := filepath.Join(tmpDir, "work")
		if err := os.MkdirAll(work, 0777); err != nil {
			t.Fatal(err)
		}

		defer testutil.DumpDir(t, tmpDir)
		fn(ctx, t, snapshotter, work)
	}
}

// checkSnapshotterBasic tests the basic workflow of a snapshot snapshotter.
func checkSnapshotterBasic(ctx context.Context, t *testing.T, snapshotter snapshot.Snapshotter, work string) {
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

	mounts, err := snapshotter.Prepare(ctx, preparing, "")
	if err != nil {
		t.Fatalf("failure reason: %+v", err)
	}

	if len(mounts) < 1 {
		t.Fatal("expected mounts to have entries")
	}

	if err := mount.MountAll(mounts, preparing); err != nil {
		t.Fatalf("failure reason: %+v", err)
	}
	defer testutil.Unmount(t, preparing)

	if err := initialApplier.Apply(preparing); err != nil {
		t.Fatalf("failure reason: %+v", err)
	}

	committed := filepath.Join(work, "committed")
	if err := snapshotter.Commit(ctx, committed, preparing); err != nil {
		t.Fatalf("failure reason: %+v", err)
	}

	si, err := snapshotter.Stat(ctx, committed)
	if err != nil {
		t.Fatalf("failure reason: %+v", err)
	}

	assert.Equal(t, "", si.Parent)
	assert.Equal(t, snapshot.KindCommitted, si.Kind)

	next := filepath.Join(work, "nextlayer")
	if err := os.MkdirAll(next, 0777); err != nil {
		t.Fatalf("failure reason: %+v", err)
	}

	mounts, err = snapshotter.Prepare(ctx, next, committed)
	if err != nil {
		t.Fatalf("failure reason: %+v", err)
	}
	if err := mount.MountAll(mounts, next); err != nil {
		t.Fatalf("failure reason: %+v", err)
	}
	defer testutil.Unmount(t, next)

	if err := fstest.CheckDirectoryEqualWithApplier(next, initialApplier); err != nil {
		t.Fatalf("failure reason: %+v", err)
	}

	if err := diffApplier.Apply(next); err != nil {
		t.Fatalf("failure reason: %+v", err)
	}

	ni, err := snapshotter.Stat(ctx, next)
	if err != nil {
		t.Fatal(err)
	}

	assert.Equal(t, committed, ni.Parent)
	assert.Equal(t, snapshot.KindActive, ni.Kind)

	nextCommitted := filepath.Join(work, "committed-next")
	if err := snapshotter.Commit(ctx, nextCommitted, next); err != nil {
		t.Fatalf("failure reason: %+v", err)
	}

	si2, err := snapshotter.Stat(ctx, nextCommitted)
	if err != nil {
		t.Fatalf("failure reason: %+v", err)
	}

	assert.Equal(t, committed, si2.Parent)
	assert.Equal(t, snapshot.KindCommitted, si2.Kind)

	expected := map[string]snapshot.Info{
		si.Name:  si,
		si2.Name: si2,
	}
	walked := map[string]snapshot.Info{} // walk is not ordered
	assert.NoError(t, snapshotter.Walk(ctx, func(ctx context.Context, si snapshot.Info) error {
		walked[si.Name] = si
		return nil
	}))

	assert.Equal(t, expected, walked)

	nextnext := filepath.Join(work, "nextnextlayer")
	if err := os.MkdirAll(nextnext, 0777); err != nil {
		t.Fatalf("failure reason: %+v", err)
	}

	mounts, err = snapshotter.View(ctx, nextnext, nextCommitted)
	if err != nil {
		t.Fatalf("failure reason: %+v", err)
	}
	if err := mount.MountAll(mounts, nextnext); err != nil {
		t.Fatalf("failure reason: %+v", err)
	}
	defer testutil.Unmount(t, nextnext)

	if err := fstest.CheckDirectoryEqualWithApplier(nextnext,
		fstest.Apply(initialApplier, diffApplier)); err != nil {
		t.Fatalf("failure reason: %+v", err)
	}

	assert.NoError(t, snapshotter.Remove(ctx, nextnext))
	assert.Error(t, snapshotter.Remove(ctx, committed))
	assert.NoError(t, snapshotter.Remove(ctx, nextCommitted))
	assert.NoError(t, snapshotter.Remove(ctx, committed))
}

// Create a New Layer on top of base layer with Prepare, Stat on new layer, should return Active layer.
func checkSnapshotterStatActive(ctx context.Context, t *testing.T, snapshotter snapshot.Snapshotter, work string) {
	preparing := filepath.Join(work, "preparing")
	if err := os.MkdirAll(preparing, 0777); err != nil {
		t.Fatal(err)
	}

	mounts, err := snapshotter.Prepare(ctx, preparing, "")
	if err != nil {
		t.Fatal(err)
	}

	if len(mounts) < 1 {
		t.Fatal("expected mounts to have entries")
	}

	if err = mount.MountAll(mounts, preparing); err != nil {
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
	assert.Equal(t, si.Name, preparing)
	assert.Equal(t, snapshot.KindActive, si.Kind)
	assert.Equal(t, "", si.Parent)
}

// Commit a New Layer on top of base layer with Prepare & Commit , Stat on new layer, should return Committed layer.
func checkSnapshotterStatCommitted(ctx context.Context, t *testing.T, snapshotter snapshot.Snapshotter, work string) {
	preparing := filepath.Join(work, "preparing")
	if err := os.MkdirAll(preparing, 0777); err != nil {
		t.Fatal(err)
	}

	mounts, err := snapshotter.Prepare(ctx, preparing, "")
	if err != nil {
		t.Fatal(err)
	}

	if len(mounts) < 1 {
		t.Fatal("expected mounts to have entries")
	}

	if err = mount.MountAll(mounts, preparing); err != nil {
		t.Fatal(err)
	}
	defer testutil.Unmount(t, preparing)

	if err = ioutil.WriteFile(filepath.Join(preparing, "foo"), []byte("foo\n"), 0777); err != nil {
		t.Fatal(err)
	}

	committed := filepath.Join(work, "committed")
	if err = snapshotter.Commit(ctx, committed, preparing); err != nil {
		t.Fatal(err)
	}

	si, err := snapshotter.Stat(ctx, committed)
	if err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, si.Name, committed)
	assert.Equal(t, snapshot.KindCommitted, si.Kind)
	assert.Equal(t, "", si.Parent)

}

func snapshotterPrepareMount(ctx context.Context, snapshotter snapshot.Snapshotter, diffPathName string, parent string, work string) (string, error) {
	preparing := filepath.Join(work, diffPathName)
	if err := os.MkdirAll(preparing, 0777); err != nil {
		return "", err
	}

	mounts, err := snapshotter.Prepare(ctx, preparing, parent)
	if err != nil {
		return "", err
	}

	if len(mounts) < 1 {
		return "", fmt.Errorf("expected mounts to have entries")
	}

	if err = mount.MountAll(mounts, preparing); err != nil {
		return "", err
	}
	return preparing, nil
}

// Given A <- B <- C, B is the parent of C and A is a transitive parent of C (in this case, a "grandparent")
func checkSnapshotterTransitivity(ctx context.Context, t *testing.T, snapshotter snapshot.Snapshotter, work string) {
	preparing, err := snapshotterPrepareMount(ctx, snapshotter, "preparing", "", work)
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Unmount(t, preparing)

	if err = ioutil.WriteFile(filepath.Join(preparing, "foo"), []byte("foo\n"), 0777); err != nil {
		t.Fatal(err)
	}

	snapA := filepath.Join(work, "snapA")
	if err = snapshotter.Commit(ctx, snapA, preparing); err != nil {
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
	if err = snapshotter.Commit(ctx, snapB, next); err != nil {
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
	assert.Equal(t, "", siA.Parent)
	assert.Equal(t, snapA, siB.Parent)
	assert.Equal(t, "", siParentB.Parent)

}

// Creating two layers with Prepare or View with same key must fail.
func checkSnapshotterPrepareView(ctx context.Context, t *testing.T, snapshotter snapshot.Snapshotter, work string) {
	preparing, err := snapshotterPrepareMount(ctx, snapshotter, "preparing", "", work)
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Unmount(t, preparing)

	snapA := filepath.Join(work, "snapA")
	if err = snapshotter.Commit(ctx, snapA, preparing); err != nil {
		t.Fatal(err)
	}

	// Prepare & View with same key
	newLayer := filepath.Join(work, "newlayer")
	if err = os.MkdirAll(preparing, 0777); err != nil {
		t.Fatal(err)
	}

	// Prepare & View with same key
	_, err = snapshotter.Prepare(ctx, newLayer, snapA)
	if err != nil {
		t.Fatal(err)
	}

	_, err = snapshotter.View(ctx, newLayer, snapA)
	//must be err != nil
	assert.NotNil(t, err)

	// Two Prepare with same key
	prepLayer := filepath.Join(work, "prepLayer")
	if err = os.MkdirAll(preparing, 0777); err != nil {
		t.Fatal(err)
	}

	_, err = snapshotter.Prepare(ctx, prepLayer, snapA)
	if err != nil {
		t.Fatal(err)
	}

	_, err = snapshotter.Prepare(ctx, prepLayer, snapA)
	//must be err != nil
	assert.NotNil(t, err)

	// Two View with same key
	viewLayer := filepath.Join(work, "viewLayer")
	if err = os.MkdirAll(preparing, 0777); err != nil {
		t.Fatal(err)
	}

	_, err = snapshotter.View(ctx, viewLayer, snapA)
	if err != nil {
		t.Fatal(err)
	}

	_, err = snapshotter.View(ctx, viewLayer, snapA)
	//must be err != nil
	assert.NotNil(t, err)

}
