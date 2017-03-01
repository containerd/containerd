package snapshot

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"github.com/docker/containerd"
	"github.com/docker/containerd/testutil"
	"github.com/stretchr/testify/assert"
)

// Create a New Layer on top of base layer with Prepare, Stat on new layer, should return Active layer.
func checkSnapshotterStatActive(ctx context.Context, t *testing.T, snapshotter Snapshotter, work string) {
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

	if err = containerd.MountAll(mounts, preparing); err != nil {
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
	assert.Equal(t, KindActive, si.Kind)
	assert.Equal(t, "", si.Parent)
}

// Commit a New Layer on top of base layer with Prepare & Commit , Stat on new layer, should return Committed layer.
func checkSnapshotterStatCommitted(ctx context.Context, t *testing.T, snapshotter Snapshotter, work string) {
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

	if err = containerd.MountAll(mounts, preparing); err != nil {
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
	assert.Equal(t, KindCommitted, si.Kind)
	assert.Equal(t, "", si.Parent)

}

func snapshotterPrepareMount(ctx context.Context, snapshotter Snapshotter, diffPathName string, parent string, work string) (string, error) {
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

	if err = containerd.MountAll(mounts, preparing); err != nil {
		return "", err
	}
	return preparing, nil
}

// Given A <- B <- C, B is the parent of C and A is a transitive parent of C (in this case, a "grandparent")
func checkSnapshotterTransitivity(t *testing.T, snapshotter Snapshotter, work string) {
	ctx := context.TODO()
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
