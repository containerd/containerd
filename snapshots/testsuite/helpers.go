/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package testsuite

import (
	"context"
	"fmt"
	"io/ioutil"
	"math/rand"
	"os"

	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/snapshots"
	"github.com/containerd/continuity/fs/fstest"
	"github.com/pkg/errors"
)

func applyToMounts(m []mount.Mount, work string, a fstest.Applier) (err error) {
	td, err := ioutil.TempDir(work, "prepare")
	if err != nil {
		return errors.Wrap(err, "failed to create temp dir")
	}
	defer os.RemoveAll(td)

	if err := mount.All(m, td); err != nil {
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
func createSnapshot(ctx context.Context, sn snapshots.Snapshotter, parent, work string, a fstest.Applier) (string, error) {
	n := fmt.Sprintf("%p-%d", a, rand.Int())
	prepare := fmt.Sprintf("%s-prepare", n)

	m, err := sn.Prepare(ctx, prepare, parent, opt)
	if err != nil {
		return "", errors.Wrap(err, "failed to prepare snapshot")
	}

	if err := applyToMounts(m, work, a); err != nil {
		return "", errors.Wrap(err, "failed to apply")
	}

	if err := sn.Commit(ctx, n, prepare, opt); err != nil {
		return "", errors.Wrap(err, "failed to commit")
	}

	return n, nil
}

// createBaseSnapshot creates a new base snapshot in the snapshotter
// given an applier to run, and applies base processing if necessary.
func createBaseSnapshot(ctx context.Context, sn snapshots.Snapshotter, work string, a fstest.Applier) (string, error) {
	n := fmt.Sprintf("%p-%d", a, rand.Int())
	prepare := fmt.Sprintf("%s-prepare", n)

	m, err := sn.Prepare(ctx, prepare, "", opt)
	if err != nil {
		return "", errors.Wrap(err, "failed to prepare snapshot")
	}

	if err := applyToMounts(m, work, a); err != nil {
		return "", errors.Wrap(err, "failed to apply")
	}

	if err := setupBaseSnapshot(ctx, sn, prepare); err != nil {
		return "", errors.Wrap(err, "failed to set up base snapshot")
	}

	if err := sn.Commit(ctx, n, prepare, opt); err != nil {
		return "", errors.Wrap(err, "failed to commit")
	}

	return n, nil
}

func checkSnapshot(ctx context.Context, sn snapshots.Snapshotter, work, name, check string) (err error) {
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
	m, err := sn.View(ctx, view, name, opt)
	if err != nil {
		return errors.Wrap(err, "failed to create view")
	}
	defer func() {
		if err1 := sn.Remove(ctx, view); err == nil {
			err = errors.Wrap(err1, "failed to remove view")
		}
	}()

	if err := mount.All(m, td); err != nil {
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
func checkSnapshots(ctx context.Context, sn snapshots.Snapshotter, work string, as ...fstest.Applier) error {
	td, err := ioutil.TempDir(work, "flat")
	if err != nil {
		return errors.Wrap(err, "failed to create temp dir")
	}
	defer os.RemoveAll(td)

	var parentID string
	for i, a := range as {
		var (
			s   string
			err error
		)
		if i == 0 {
			s, err = createBaseSnapshot(ctx, sn, work, a)
			if err != nil {
				return errors.Wrapf(err, "failed to create base snapshot %d", i+1)
			}
		} else {
			s, err = createSnapshot(ctx, sn, parentID, work, a)
			if err != nil {
				return errors.Wrapf(err, "failed to create snapshot %d", i+1)
			}
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

// checkInfo checks that the infos are the same
func checkInfo(si1, si2 snapshots.Info) error {
	if si1.Kind != si2.Kind {
		return errors.Errorf("Expected kind %v, got %v", si1.Kind, si2.Kind)
	}
	if si1.Name != si2.Name {
		return errors.Errorf("Expected name %v, got %v", si1.Name, si2.Name)
	}
	if si1.Parent != si2.Parent {
		return errors.Errorf("Expected Parent %v, got %v", si1.Parent, si2.Parent)
	}
	if len(si1.Labels) != len(si2.Labels) {
		return errors.Errorf("Expected %d labels, got %d", len(si1.Labels), len(si2.Labels))
	}
	for k, l1 := range si1.Labels {
		l2 := si2.Labels[k]
		if l1 != l2 {
			return errors.Errorf("Expected label %v, got %v", l1, l2)
		}
	}
	if si1.Created != si2.Created {
		return errors.Errorf("Expected Created %v, got %v", si1.Created, si2.Created)
	}
	if si1.Updated != si2.Updated {
		return errors.Errorf("Expected Updated %v, got %v", si1.Updated, si2.Updated)
	}

	return nil
}
