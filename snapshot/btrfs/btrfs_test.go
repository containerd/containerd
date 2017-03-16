package btrfs

import (
	"context"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/docker/containerd"
	"github.com/docker/containerd/snapshot"
	"github.com/docker/containerd/snapshot/testsuite"
	"github.com/docker/containerd/testutil"
)

const (
	mib = 1024 * 1024
)

func TestBtrfs(t *testing.T) {
	testutil.RequiresRoot(t)
	testsuite.SnapshotterSuite(t, "Btrfs", func(ctx context.Context, root string) (snapshot.Snapshotter, func(), error) {
		device := setupBtrfsLoopbackDevice(t, root)
		snapshotter, err := NewSnapshotter(device.deviceName, root)
		if err != nil {
			t.Fatal(err)
		}

		return snapshotter, func() {
			device.remove(t)
		}, nil
	})
}

func TestBtrfsMounts(t *testing.T) {
	testutil.RequiresRoot(t)
	ctx := context.TODO()

	// create temporary directory for mount point
	mountPoint, err := ioutil.TempDir("", "containerd-btrfs-test")
	if err != nil {
		t.Fatal("could not create mount point for btrfs test", err)
	}
	t.Log("temporary mount point created", mountPoint)

	device := setupBtrfsLoopbackDevice(t, mountPoint)
	defer device.remove(t)

	root, err := ioutil.TempDir(device.mountPoint, "TestBtrfsPrepare-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)

	target := filepath.Join(root, "test")
	b, err := NewSnapshotter(device.deviceName, root)
	if err != nil {
		t.Fatal(err)
	}
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
	if err := containerd.MountAll(mounts, target); err != nil {
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

	if err := containerd.MountAll(mounts, target); err != nil {
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

type testDevice struct {
	mountPoint string
	fileName   string
	deviceName string
}

// setupBtrfsLoopbackDevice creates a file, mounts it as a loopback device, and
// formats it as btrfs.  The device should be cleaned up by calling
// removeBtrfsLoopbackDevice.
func setupBtrfsLoopbackDevice(t *testing.T, mountPoint string) *testDevice {

	// create temporary file for the disk image
	file, err := ioutil.TempFile("", "containerd-btrfs-test")
	if err != nil {
		t.Fatal("Could not create temporary file for btrfs test", err)
	}
	t.Log("Temporary file created", file.Name())

	// initialize file with 100 MiB
	if err := file.Truncate(100 << 20); err != nil {
		t.Fatal(err)
	}
	file.Close()

	// create device
	losetup := exec.Command("losetup", "--find", "--show", file.Name())
	p, err := losetup.Output()
	if err != nil {
		t.Fatal(err)
	}

	deviceName := strings.TrimSpace(string(p))
	t.Log("Created loop device", deviceName)

	// format
	t.Log("Creating btrfs filesystem")
	mkfs := exec.Command("mkfs.btrfs", deviceName)
	err = mkfs.Run()
	if err != nil {
		t.Fatal("Could not run mkfs.btrfs", err)
	}

	// mount
	t.Logf("Mounting %s at %s", deviceName, mountPoint)
	mount := exec.Command("mount", deviceName, mountPoint)
	err = mount.Run()
	if err != nil {
		t.Fatal("Could not mount", err)
	}

	return &testDevice{
		mountPoint: mountPoint,
		fileName:   file.Name(),
		deviceName: deviceName,
	}
}

// remove cleans up the test device, unmounting the loopback and disk image
// file.
func (device *testDevice) remove(t *testing.T) {
	// unmount
	testutil.Unmount(t, device.mountPoint)

	// detach device
	t.Log("Removing loop device")
	losetup := exec.Command("losetup", "--detach", device.deviceName)
	err := losetup.Run()
	if err != nil {
		t.Error("Could not remove loop device", device.deviceName, err)
	}

	// remove file
	t.Log("Removing temporary file")
	err = os.Remove(device.fileName)
	if err != nil {
		t.Error(err)
	}

	// remove mount point
	t.Log("Removing temporary mount point")
	err = os.RemoveAll(device.mountPoint)
	if err != nil {
		t.Error(err)
	}
}
