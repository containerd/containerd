package testsuite

import (
	"context"
	"fmt"
	"io/ioutil"
	"math/rand"
	"os"

	"github.com/containerd/containerd/fs/fstest"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/snapshot"
	"github.com/pkg/errors"
)

func applyToMounts(m []mount.Mount, work string, a fstest.Applier) (err error) {
	td, err := ioutil.TempDir(work, "prepare")
	if err != nil {
		return errors.Wrap(err, "failed to create temp dir")
	}
	defer os.RemoveAll(td)

	if err := mount.MountAll(m, td); err != nil {
		return errors.Wrap(err, "failed to mount")
	}
	defer func() {
		if err1 := mount.UnmountAll(td, umountflags); err == nil {
			err = errors.Wrap(err1, "failed to unmount")
		}
	}()

	return a.Apply(td)
}

// createSnapshot creates a new snapshot in the snapshotter
// given an applier to run on top of the given parent.
func createSnapshot(ctx context.Context, sn snapshot.Snapshotter, parent, work string, a fstest.Applier) (string, error) {
	n := fmt.Sprintf("%p-%d", a, rand.Int())
	prepare := fmt.Sprintf("%s-prepare", n)

	m, err := sn.Prepare(ctx, prepare, parent)
	if err != nil {
		return "", errors.Wrap(err, "failed to prepare snapshot")
	}

	if err := applyToMounts(m, work, a); err != nil {
		return "", errors.Wrap(err, "failed to apply")
	}

	if err := sn.Commit(ctx, n, prepare); err != nil {
		return "", errors.Wrap(err, "failed to commit")
	}

	return n, nil
}

func checkSnapshot(ctx context.Context, sn snapshot.Snapshotter, work, name, check string) (err error) {
	td, err := ioutil.TempDir(work, "check")
	if err != nil {
		return errors.Wrap(err, "failed to create temp dir")
	}
	defer func() {
		if err1 := os.RemoveAll(td); err == nil {
			err = errors.Wrapf(err1, "failed to remove temporary directory %s", td)
		}
	}()

	view := fmt.Sprintf("%s-view", name)
	m, err := sn.View(ctx, view, name)
	if err != nil {
		return errors.Wrap(err, "failed to create view")
	}
	defer func() {
		if err1 := sn.Remove(ctx, view); err == nil {
			err = errors.Wrap(err1, "failed to remove view")
		}
	}()

	if err := mount.MountAll(m, td); err != nil {
		return errors.Wrap(err, "failed to mount")
	}
	defer func() {
		if err1 := mount.UnmountAll(td, umountflags); err == nil {
			err = errors.Wrap(err1, "failed to unmount view")
		}
	}()

	if err := fstest.CheckDirectoryEqual(check, td); err != nil {
		return errors.Wrap(err, "check directory failed")
	}

	return nil
}

// checkSnapshots creates a new chain of snapshots in the given snapshotter
// using the provided appliers, checking each snapshot created in a view
// against the changes applied to a single directory.
func checkSnapshots(ctx context.Context, sn snapshot.Snapshotter, work string, as ...fstest.Applier) error {
	td, err := ioutil.TempDir(work, "flat")
	if err != nil {
		return errors.Wrap(err, "failed to create temp dir")
	}
	defer os.RemoveAll(td)

	var parentID string
	for i, a := range as {
		s, err := createSnapshot(ctx, sn, parentID, work, a)
		if err != nil {
			return errors.Wrapf(err, "failed to create snapshot %d", i+1)
		}

		if err := a.Apply(td); err != nil {
			return errors.Wrapf(err, "failed to apply to check directory on %d", i+1)
		}

		if err := checkSnapshot(ctx, sn, work, s, td); err != nil {
			return errors.Wrapf(err, "snapshot check failed on snapshot %d", i+1)
		}

		parentID = s
	}
	return nil

}
