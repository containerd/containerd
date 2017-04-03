package naive

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/testutil"
)

func TestSnapshotNaiveBasic(t *testing.T) {
	testutil.RequiresRoot(t)
	tmpDir, err := ioutil.TempDir("", "test-naive-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	t.Log(tmpDir)
	root := filepath.Join(tmpDir, "root")

	n, err := NewNaive(root)
	if err != nil {
		t.Fatal(err)
	}

	preparing := filepath.Join(tmpDir, "preparing")
	if err := os.MkdirAll(preparing, 0777); err != nil {
		t.Fatal(err)
	}

	mounts, err := n.Prepare(preparing, "")
	if err != nil {
		t.Fatal(err)
	}

	if err := containerd.MountAll(mounts, preparing); err != nil {
		t.Fatal(err)
	}

	if err := ioutil.WriteFile(filepath.Join(preparing, "foo"), []byte("foo\n"), 0777); err != nil {
		t.Fatal(err)
	}

	os.MkdirAll(preparing+"/a/b/c", 0755)

	// defer os.Remove(filepath.Join(tmpDir, "foo"))

	committed := filepath.Join(n.root, "committed")

	if err := n.Commit(committed, preparing); err != nil {
		t.Fatal(err)
	}

	if n.Parent(preparing) != "" {
		t.Fatalf("parent of new layer should be empty, got n.Parent(%q) == %q", preparing, n.Parent(preparing))
	}

	next := filepath.Join(tmpDir, "nextlayer")
	if err := os.MkdirAll(next, 0777); err != nil {
		t.Fatal(err)
	}

	mounts, err = n.Prepare(next, committed)
	if err != nil {
		t.Fatal(err)
	}
	if err := containerd.MountAll(mounts, next); err != nil {
		t.Fatal(err)
	}

	if err := ioutil.WriteFile(filepath.Join(next, "bar"), []byte("bar\n"), 0777); err != nil {
		t.Fatal(err)
	}

	// also, change content of foo to bar
	if err := ioutil.WriteFile(filepath.Join(next, "foo"), []byte("bar\n"), 0777); err != nil {
		t.Fatal(err)
	}

	os.RemoveAll(next + "/a/b")
	nextCommitted := filepath.Join(n.root, "committed-next")
	if err := n.Commit(nextCommitted, next); err != nil {
		t.Fatal(err)
	}

	if n.Parent(nextCommitted) != committed {
		t.Fatalf("parent of new layer should be %q, got n.Parent(%q) == %q (%#v)", committed, next, n.Parent(next), n.parents)
	}
}
