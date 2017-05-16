// +build linux freebsd

package zfs

import (
	"context"
	"flag"
	"io/ioutil"
	"os"
	"path/filepath"

	"testing"

	"github.com/containerd/containerd/snapshot"
	"github.com/containerd/containerd/snapshot/testsuite"
	"github.com/containerd/containerd/testutil"
	zfs "github.com/mistifyio/go-zfs"
)

var testZPool string

func init() {
	flag.StringVar(&testZPool, "test.zpool", "", "run the test using the specified zpool")
}

func boltSnapshotter(t *testing.T) func(context.Context, string) (snapshot.Snapshotter, func(), error) {
	return func(ctx context.Context, root string) (snapshot.Snapshotter, func(), error) {
		if testZPool == "" {
			t.Skip("Please set -test.zpool. TODO: create a loopback zfs image.")
		}
		testZFSMountpoint, err := ioutil.TempDir("", "containerd-zfs-test")
		if err != nil {
			t.Fatal(err)
		}
		testZFSName := filepath.Join(testZPool, "containerd-zfs-test")
		// FIXME: this test panicks if testZPool is set to invalid non-empty zpool name
		testZFS, err := zfs.CreateFilesystem(testZFSName, map[string]string{
			"mountpoint": testZFSMountpoint,
		})
		if err != nil {
			t.Fatalf("could not create zfs %q on %q: %v", testZFSName, testZFSMountpoint, err)
		}
		snapshotter, err := NewSnapshotter(testZFSMountpoint)
		if err != nil {
			t.Fatal(err)
		}

		return snapshotter, func() {
			if err := testZFS.Destroy(zfs.DestroyRecursive | zfs.DestroyRecursiveClones | zfs.DestroyForceUmount); err != nil {
				t.Fatal(err)
			}
			if err := os.RemoveAll(testZFSMountpoint); err != nil {
				t.Fatal(err)
			}
		}, nil
	}
}

func TestZFS(t *testing.T) {
	testutil.RequiresRoot(t)
	testsuite.SnapshotterSuite(t, "zfs", boltSnapshotter(t))
}
