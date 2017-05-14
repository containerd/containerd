// +build linux

package btrfs

import (
	"context"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/snapshot"
	"github.com/containerd/containerd/snapshot/testsuite"
	"github.com/containerd/containerd/testutil"
)

func boltSnapshotter(t *testing.T) func(context.Context, string) (snapshot.Snapshotter, func(), error) {
	return func(ctx context.Context, root string) (snapshot.Snapshotter, func(), error) {

		deviceName, cleanupDevice := testutil.NewLoopback(t, 100<<20) // 100 MB

		if out, err := exec.Command("mkfs.btrfs", deviceName).CombinedOutput(); err != nil {
			// not fatal
			t.Skipf("could not mkfs.btrfs %s: %v (out: %q)", deviceName, err, string(out))
		}
		if out, err := exec.Command("mount", deviceName, root).CombinedOutput(); err != nil {
			// not fatal
			t.Skipf("could not mount %s: %v (out: %q)", deviceName, err, string(out))
		}

		snapshotter, err := NewSnapshotter(root)
		if err != nil {
			t.Fatal(err)
		}

		return snapshotter, func() {
			testutil.Unmount(t, root)
			cleanupDevice()
		}, nil
	}
}

func TestBtrfs(t *testing.T) {
	testutil.RequiresRoot(t)
	testsuite.SnapshotterSuite(t, "Btrfs", boltSnapshotter(t))
}

func TestBtrfsMounts(t *testing.T) {
	testutil.RequiresRoot(t)
	ctx := context.Background()

	// create temporary directory for mount point
	mountPoint, err := ioutil.TempDir("", "containerd-btrfs-test")
	if err != nil {
		t.Fatal("could not create mount point for btrfs test", err)
	}
	defer os.RemoveAll(mountPoint)
	t.Log("temporary mount point created", mountPoint)

	root, err := ioutil.TempDir(mountPoint, "TestBtrfsPrepare-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)

	b, c, err := boltSnapshotter(t)(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer c()

	target := filepath.Join(root, "test")
	mounts, err := b.Prepare(ctx, target, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Log(mounts)

	for _, mount := range mounts {
		if mount.Type != "btrfs" {
			t.Fatalf("wrong mount type: %v != btrfs", mount.Type)
		}

		// assumes the first, maybe incorrect in the future
		if !strings.HasPrefix(mount.Options[0], "subvolid=") {
			t.Fatalf("no subvolid option in %v", mount.Options)
		}
	}

	if err := os.MkdirAll(target, 0755); err != nil {
		t.Fatal(err)
	}
	if err := mount.MountAll(mounts, target); err != nil {
		t.Fatal(err)
	}
	defer testutil.Unmount(t, target)

	// write in some data
	if err := ioutil.WriteFile(filepath.Join(target, "foo"), []byte("content"), 0777); err != nil {
		t.Fatal(err)
	}

	// TODO(stevvooe): We don't really make this with the driver, but that
	// might prove annoying in practice.
	if err := os.MkdirAll(filepath.Join(root, "snapshots"), 0755); err != nil {
		t.Fatal(err)
	}

	if err := b.Commit(ctx, filepath.Join(root, "snapshots/committed"), filepath.Join(root, "test")); err != nil {
		t.Fatal(err)
	}

	target = filepath.Join(root, "test2")
	mounts, err = b.Prepare(ctx, target, filepath.Join(root, "snapshots/committed"))
	if err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(target, 0755); err != nil {
		t.Fatal(err)
	}

	if err := mount.MountAll(mounts, target); err != nil {
		t.Fatal(err)
	}
	defer testutil.Unmount(t, target)

	// TODO(stevvooe): Verify contents of "foo"
	if err := ioutil.WriteFile(filepath.Join(target, "bar"), []byte("content"), 0777); err != nil {
		t.Fatal(err)
	}

	if err := b.Commit(ctx, filepath.Join(root, "snapshots/committed2"), target); err != nil {
		t.Fatal(err)
	}
}
