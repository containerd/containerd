package snapshot

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"github.com/docker/containerd"
	"github.com/docker/containerd/snapshot/testutil"
)

// TestSnapshotManagerBasic implements something similar to the conceptual
// examples we've discussed thus far. It does perform mounts, so you must run
// as root.
func TestSnapshotManagerBasic(t *testing.T) {
	tmpDir, err := ioutil.TempDir("", "test-sm-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		t.Log("Removing", tmpDir)
		err := os.RemoveAll(tmpDir)
		if err != nil {
			t.Error(err)
		}
	}()

	root := filepath.Join(tmpDir, "root")

	sm, err := NewManager(root)
	if err != nil {
		t.Fatal(err)
	}

	preparing := filepath.Join(tmpDir, "preparing")
	if err := os.MkdirAll(preparing, 0777); err != nil {
		t.Fatal(err)
	}

	mounts, err := sm.Prepare(preparing, "")
	if err != nil {
		t.Fatal(err)
	}

	if len(mounts) < 1 {
		t.Fatal("expected mounts to have entries")
	}

	if err := containerd.MountAll(mounts, preparing); err != nil {
		t.Fatal(err)
	}
	defer testutil.Unmount(t, preparing)

	if err := ioutil.WriteFile(filepath.Join(preparing, "foo"), []byte("foo\n"), 0777); err != nil {
		t.Fatal(err)
	}

	os.MkdirAll(preparing+"/a/b/c", 0755)

	committed := filepath.Join(sm.root, "committed")

	if err := sm.Commit(committed, preparing); err != nil {
		t.Fatal(err)
	}

	if sm.Parent(preparing) != "" {
		t.Fatalf("parent of new layer should be empty, got sm.Parent(%q) == %q", preparing, sm.Parent(preparing))
	}

	next := filepath.Join(tmpDir, "nextlayer")
	if err := os.MkdirAll(next, 0777); err != nil {
		t.Fatal(err)
	}

	mounts, err = sm.Prepare(next, committed)
	if err != nil {
		t.Fatal(err)
	}
	if err := containerd.MountAll(mounts, next); err != nil {
		t.Fatal(err)
	}
	defer testutil.Unmount(t, next)

	if err := ioutil.WriteFile(filepath.Join(next, "bar"), []byte("bar\n"), 0777); err != nil {
		t.Fatal(err)
	}

	// also, change content of foo to bar
	if err := ioutil.WriteFile(filepath.Join(next, "foo"), []byte("bar\n"), 0777); err != nil {
		t.Fatal(err)
	}

	os.RemoveAll(next + "/a/b")
	nextCommitted := filepath.Join(sm.root, "committed-next")
	if err := sm.Commit(nextCommitted, next); err != nil {
		t.Fatal(err)
	}

	if sm.Parent(nextCommitted) != committed {
		t.Fatalf("parent of new layer should be %q, got sm.Parent(%q) == %q (%#v)", committed, next, sm.Parent(next), sm.parents)
	}
}
