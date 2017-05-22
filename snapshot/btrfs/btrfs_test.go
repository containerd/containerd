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

const (
	mib = 1024 * 1024
)

func boltSnapshotter(t *testing.T) func(context.Context, string) (snapshot.Snapshotter, func(), error) {
	return func(ctx context.Context, root string) (snapshot.Snapshotter, func(), error) {
		device := setupBtrfsLoopbackDevice(t, root)
		snapshotter, err := NewSnapshotter(root)
		if err != nil {
			t.Fatal(err)
		}

		return snapshotter, func() {
			device.remove(t)
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

func TestGetBtrfsDevice(t *testing.T) {
	testCases := []struct {
		expectedDevice string
		expectedError  string
		root           string
		mounts         []mount.Info
	}{
		{
			expectedDevice: "/dev/loop0",
			root:           "/var/lib/containerd/snapshot/btrfs",
			mounts: []mount.Info{
				{Root: "/", Mountpoint: "/", FSType: "ext4", Source: "/dev/sda1"},
				{Root: "/", Mountpoint: "/var/lib/containerd/snapshot/btrfs", FSType: "btrfs", Source: "/dev/loop0"},
			},
		},
		{
			expectedError: "/var/lib/containerd/snapshot/btrfs is not mounted as btrfs",
			root:          "/var/lib/containerd/snapshot/btrfs",
			mounts: []mount.Info{
				{Root: "/", Mountpoint: "/", FSType: "ext4", Source: "/dev/sda1"},
			},
		},
		{
			expectedDevice: "/dev/sda1",
			root:           "/var/lib/containerd/snapshot/btrfs",
			mounts: []mount.Info{
				{Root: "/", Mountpoint: "/", FSType: "btrfs", Source: "/dev/sda1"},
			},
		},
		{
			expectedDevice: "/dev/sda2",
			root:           "/var/lib/containerd/snapshot/btrfs",
			mounts: []mount.Info{
				{Root: "/", Mountpoint: "/", FSType: "btrfs", Source: "/dev/sda1"},
				{Root: "/", Mountpoint: "/var/lib/containerd/snapshot/btrfs", FSType: "btrfs", Source: "/dev/sda2"},
			},
		},
		{
			expectedDevice: "/dev/sda2",
			root:           "/var/lib/containerd/snapshot/btrfs",
			mounts: []mount.Info{
				{Root: "/", Mountpoint: "/var/lib/containerd/snapshot/btrfs", FSType: "btrfs", Source: "/dev/sda2"},
				{Root: "/", Mountpoint: "/var/lib/foooooooooooooooooooo/baaaaaaaaaaaaaaaaaaaar", FSType: "btrfs", Source: "/dev/sda3"}, // mountpoint length longer than /var/lib/containerd/snapshot/btrfs
				{Root: "/", Mountpoint: "/", FSType: "btrfs", Source: "/dev/sda1"},
			},
		},
	}
	for i, tc := range testCases {
		device, err := getBtrfsDevice(tc.root, tc.mounts)
		if err != nil && tc.expectedError == "" {
			t.Fatalf("%d: expected nil, got %v", i, err)
		}
		if err != nil && !strings.Contains(err.Error(), tc.expectedError) {
			t.Fatalf("%d: expected %s, got %v", i, tc.expectedError, err)
		}
		if err == nil && device != tc.expectedDevice {
			t.Fatalf("%d: expected %s, got %s", i, tc.expectedDevice, device)
		}
	}
}
