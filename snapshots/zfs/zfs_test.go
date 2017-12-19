// +build linux

package zfs

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/containerd/containerd/snapshots"
	"github.com/containerd/containerd/snapshots/testsuite"
	"github.com/containerd/containerd/testutil"
	zfs "github.com/mistifyio/go-zfs"
	"github.com/pkg/errors"
)

func newTestZpool() (string, func() error, error) {
	lo, destroyLo, err := testutil.NewLoopback(1 << 30) // 1GiB
	if err != nil {
		return "", nil, err
	}
	zpoolName := fmt.Sprintf("testzpool-%d", time.Now().UnixNano())
	zpool, err := zfs.CreateZpool(zpoolName, nil, lo)
	if err != nil {
		return "", nil, err
	}
	return zpoolName, func() error {
		if err := zpool.Destroy(); err != nil {
			return err
		}
		return destroyLo()
	}, nil
}

func newSnapshotter() func(context.Context, string) (snapshots.Snapshotter, func() error, error) {
	return func(ctx context.Context, root string) (snapshots.Snapshotter, func() error, error) {
		testZpool, destroyTestZpool, err := newTestZpool()
		if err != nil {
			return nil, nil, err
		}
		testZFSMountpoint, err := ioutil.TempDir("", "containerd-zfs-test")
		if err != nil {
			return nil, nil, err
		}
		testZFSName := filepath.Join(testZpool, "containerd-zfs-test")
		testZFS, err := zfs.CreateFilesystem(testZFSName, map[string]string{
			"mountpoint": testZFSMountpoint,
		})
		if err != nil {
			return nil, nil, errors.Wrapf(err, "could not create zfs %q on %q", testZFSName, testZFSMountpoint)
		}
		snapshotter, err := NewSnapshotter(testZFSMountpoint)
		if err != nil {
			return nil, nil, err
		}

		return snapshotter, func() error {
			if err := snapshotter.Close(); err != nil {
				return err
			}
			if err := testZFS.Destroy(zfs.DestroyRecursive | zfs.DestroyRecursiveClones | zfs.DestroyForceUmount); err != nil {
				return err
			}
			if err := os.RemoveAll(testZFSMountpoint); err != nil {
				return err
			}
			return destroyTestZpool()
		}, nil
	}
}

func TestZFS(t *testing.T) {
	testutil.RequiresRoot(t)
	testsuite.SnapshotterSuite(t, "zfs", newSnapshotter())
}
