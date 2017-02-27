package overlay

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/docker/containerd"
	"github.com/docker/containerd/snapshot"
	"github.com/docker/containerd/testutil"
)

func TestOverlay(t *testing.T) {
	testutil.RequiresRoot(t)
	snapshot.SnapshotterSuite(t, "Overlay", func(root string) (snapshot.Snapshotter, func(), error) {
		snapshotter, err := NewSnapshotter(root)
		if err != nil {
			t.Fatal(err)
		}

		return snapshotter, func() {}, nil
	})
}

func TestOverlayMounts(t *testing.T) {
	ctx := context.TODO()
	root, err := ioutil.TempDir("", "overlay")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	o, err := NewSnapshotter(root)
	if err != nil {
		t.Error(err)
		return
	}
	mounts, err := o.Prepare(ctx, "/tmp/test", "")
	if err != nil {
		t.Error(err)
		return
	}
	if len(mounts) != 1 {
		t.Errorf("should only have 1 mount but received %d", len(mounts))
	}
	m := mounts[0]
	if m.Type != "bind" {
		t.Errorf("mount type should be bind but received %q", m.Type)
	}
	expected := filepath.Join(root, "active", hash("/tmp/test"), "fs")
	if m.Source != expected {
		t.Errorf("expected source %q but received %q", expected, m.Source)
	}
	if m.Options[0] != "rw" {
		t.Errorf("expected mount option rw but received %q", m.Options[0])
	}
	if m.Options[1] != "rbind" {
		t.Errorf("expected mount option rbind but received %q", m.Options[1])
	}
}

func TestOverlayCommit(t *testing.T) {
	ctx := context.TODO()
	root, err := ioutil.TempDir("", "overlay")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	o, err := NewSnapshotter(root)
	if err != nil {
		t.Error(err)
		return
	}
	key := "/tmp/test"
	mounts, err := o.Prepare(ctx, key, "")
	if err != nil {
		t.Error(err)
		return
	}
	m := mounts[0]
	if err := ioutil.WriteFile(filepath.Join(m.Source, "foo"), []byte("hi"), 0660); err != nil {
		t.Error(err)
		return
	}
	if err := o.Commit(ctx, "base", key); err != nil {
		t.Error(err)
		return
	}
}

func TestOverlayOverlayMount(t *testing.T) {
	ctx := context.TODO()
	root, err := ioutil.TempDir("", "overlay")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	o, err := NewSnapshotter(root)
	if err != nil {
		t.Error(err)
		return
	}
	key := "/tmp/test"
	mounts, err := o.Prepare(ctx, key, "")
	if err != nil {
		t.Error(err)
		return
	}
	if err := o.Commit(ctx, "base", key); err != nil {
		t.Error(err)
		return
	}
	if mounts, err = o.Prepare(ctx, "/tmp/layer2", "base"); err != nil {
		t.Error(err)
		return
	}
	if len(mounts) != 1 {
		t.Errorf("should only have 1 mount but received %d", len(mounts))
	}
	m := mounts[0]
	if m.Type != "overlay" {
		t.Errorf("mount type should be overlay but received %q", m.Type)
	}
	if m.Source != "overlay" {
		t.Errorf("expected source %q but received %q", "overlay", m.Source)
	}
	var (
		ah    = hash("/tmp/layer2")
		sh    = hash("base")
		work  = "workdir=" + filepath.Join(root, "active", ah, "work")
		upper = "upperdir=" + filepath.Join(root, "active", ah, "fs")
		lower = "lowerdir=" + filepath.Join(root, "committed", sh, "fs")
	)
	for i, v := range []string{
		work,
		upper,
		lower,
	} {
		if m.Options[i] != v {
			t.Errorf("expected %q but received %q", v, m.Options[i])
		}
	}
}

func TestOverlayOverlayRead(t *testing.T) {
	testutil.RequiresRoot(t)
	ctx := context.TODO()
	root, err := ioutil.TempDir("", "overlay")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	o, err := NewSnapshotter(root)
	if err != nil {
		t.Error(err)
		return
	}
	key := "/tmp/test"
	mounts, err := o.Prepare(ctx, key, "")
	if err != nil {
		t.Error(err)
		return
	}
	m := mounts[0]
	if err := ioutil.WriteFile(filepath.Join(m.Source, "foo"), []byte("hi"), 0660); err != nil {
		t.Error(err)
		return
	}
	if err := o.Commit(ctx, "base", key); err != nil {
		t.Error(err)
		return
	}
	if mounts, err = o.Prepare(ctx, "/tmp/layer2", "base"); err != nil {
		t.Error(err)
		return
	}
	dest := filepath.Join(root, "dest")
	if err := os.Mkdir(dest, 0700); err != nil {
		t.Error(err)
		return
	}
	if err := containerd.MountAll(mounts, dest); err != nil {
		t.Error(err)
		return
	}
	defer syscall.Unmount(dest, 0)
	data, err := ioutil.ReadFile(filepath.Join(dest, "foo"))
	if err != nil {
		t.Error(err)
		return
	}
	if e := string(data); e != "hi" {
		t.Errorf("expected file contents hi but got %q", e)
		return
	}
}

func TestOverlayView(t *testing.T) {
	ctx := context.TODO()
	root, err := ioutil.TempDir("", "overlay")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	o, err := NewSnapshotter(root)
	if err != nil {
		t.Fatal(err)
	}
	key := "/tmp/base"
	mounts, err := o.Prepare(ctx, key, "")
	if err != nil {
		t.Fatal(err)
	}
	m := mounts[0]
	if err := ioutil.WriteFile(filepath.Join(m.Source, "foo"), []byte("hi"), 0660); err != nil {
		t.Fatal(err)
	}
	if err := o.Commit(ctx, "base", key); err != nil {
		t.Fatal(err)
	}

	key = "/tmp/top"
	_, err = o.Prepare(ctx, key, "base")
	if err != nil {
		t.Fatal(err)
	}
	if err := ioutil.WriteFile(filepath.Join(root, "active", hash(key), "fs", "foo"), []byte("hi, again"), 0660); err != nil {
		t.Fatal(err)
	}
	if err := o.Commit(ctx, "top", key); err != nil {
		t.Fatal(err)
	}

	mounts, err = o.View(ctx, "/tmp/view1", "base")
	if err != nil {
		t.Fatal(err)
	}
	if len(mounts) != 1 {
		t.Fatalf("should only have 1 mount but received %d", len(mounts))
	}
	m = mounts[0]
	if m.Type != "bind" {
		t.Errorf("mount type should be bind but received %q", m.Type)
	}
	expected := filepath.Join(root, "committed", hash("base"), "fs")
	if m.Source != expected {
		t.Errorf("expected source %q but received %q", expected, m.Source)
	}
	if m.Options[0] != "ro" {
		t.Errorf("expected mount option ro but received %q", m.Options[0])
	}
	if m.Options[1] != "rbind" {
		t.Errorf("expected mount option rbind but received %q", m.Options[1])
	}

	mounts, err = o.View(ctx, "/tmp/view2", "top")
	if err != nil {
		t.Fatal(err)
	}
	if len(mounts) != 1 {
		t.Fatalf("should only have 1 mount but received %d", len(mounts))
	}
	m = mounts[0]
	if m.Type != "overlay" {
		t.Errorf("mount type should be overlay but received %q", m.Type)
	}
	if m.Source != "overlay" {
		t.Errorf("mount source should be overlay but received %q", m.Source)
	}
	if len(m.Options) != 1 {
		t.Errorf("expected 1 mount option but got %d", len(m.Options))
	}
	expected = fmt.Sprintf("lowerdir=%s:%s", filepath.Join(root, "committed", hash("top"), "fs"), filepath.Join(root, "committed", hash("base"), "fs"))
	if m.Options[0] != expected {
		t.Errorf("expected option %q but received %q", expected, m.Options[0])
	}
}
