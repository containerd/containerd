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
	"os"

	"github.com/containerd/containerd/v2/mount"
	"github.com/containerd/containerd/v2/pkg/randutil"
	"github.com/containerd/containerd/v2/snapshots"
	"github.com/containerd/continuity/fs/fstest"
)

const umountflags int = 0

func applyToMounts(m []mount.Mount, work string, a fstest.Applier) (err error) {
	td, err := os.MkdirTemp(work, "prepare")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(td)

	if err := mount.All(m, td); err != nil {
		return fmt.Errorf("failed to mount: %w", err)
	}
	defer func() {
		if err1 := mount.UnmountAll(td, umountflags); err1 != nil && err == nil {
			err = fmt.Errorf("failed to unmount: %w", err1)
		}
	}()

	return a.Apply(td)
}

// createSnapshot creates a new snapshot in the snapshotter
// given an applier to run on top of the given parent.
func createSnapshot(ctx context.Context, sn snapshots.Snapshotter, parent, work string, a fstest.Applier) (string, error) {
	n := fmt.Sprintf("%p-%d", a, randutil.Int())
	prepare := fmt.Sprintf("%s-prepare", n)

	m, err := sn.Prepare(ctx, prepare, parent, opt)
	if err != nil {
		return "", fmt.Errorf("failed to prepare snapshot: %w", err)
	}

	if err := applyToMounts(m, work, a); err != nil {
		return "", fmt.Errorf("failed to apply: %w", err)
	}

	if err := sn.Commit(ctx, n, prepare, opt); err != nil {
		return "", fmt.Errorf("failed to commit: %w", err)
	}

	return n, nil
}

func checkSnapshot(ctx context.Context, sn snapshots.Snapshotter, work, name, check string) (err error) {
	td, err := os.MkdirTemp(work, "check")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer func() {
		if err1 := os.RemoveAll(td); err1 != nil && err == nil {
			err = fmt.Errorf("failed to remove temporary directory %s: %w", td, err1)
		}
	}()

	view := fmt.Sprintf("%s-view", name)
	m, err := sn.View(ctx, view, name, opt)
	if err != nil {
		return fmt.Errorf("failed to create view: %w", err)
	}
	defer func() {
		if err1 := sn.Remove(ctx, view); err1 != nil && err == nil {
			err = fmt.Errorf("failed to remove view: %w", err1)
		}
	}()

	if err := mount.All(m, td); err != nil {
		return fmt.Errorf("failed to mount: %w", err)
	}
	defer func() {
		if err1 := mount.UnmountAll(td, umountflags); err1 != nil && err == nil {
			err = fmt.Errorf("failed to unmount view: %w", err1)
		}
	}()

	if err := fstest.CheckDirectoryEqual(check, td); err != nil {
		return fmt.Errorf("check directory failed: %w", err)
	}

	return nil
}

// checkSnapshots creates a new chain of snapshots in the given snapshotter
// using the provided appliers, checking each snapshot created in a view
// against the changes applied to a single directory.
func checkSnapshots(ctx context.Context, sn snapshots.Snapshotter, work string, as ...fstest.Applier) error {
	td, err := os.MkdirTemp(work, "flat")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(td)

	var parentID string
	for i, a := range as {
		s, err := createSnapshot(ctx, sn, parentID, work, a)
		if err != nil {
			return fmt.Errorf("failed to create snapshot %d: %w", i+1, err)
		}

		if err := a.Apply(td); err != nil {
			return fmt.Errorf("failed to apply to check directory on %d: %w", i+1, err)
		}

		if err := checkSnapshot(ctx, sn, work, s, td); err != nil {
			return fmt.Errorf("snapshot check failed on snapshot %d: %w", i+1, err)
		}

		parentID = s
	}
	return nil

}

// checkInfo checks that the infos are the same
func checkInfo(si1, si2 snapshots.Info) error {
	if si1.Kind != si2.Kind {
		return fmt.Errorf("expected kind %v, got %v", si1.Kind, si2.Kind)
	}
	if si1.Name != si2.Name {
		return fmt.Errorf("expected name %v, got %v", si1.Name, si2.Name)
	}
	if si1.Parent != si2.Parent {
		return fmt.Errorf("expected Parent %v, got %v", si1.Parent, si2.Parent)
	}
	if len(si1.Labels) != len(si2.Labels) {
		return fmt.Errorf("expected %d labels, got %d", len(si1.Labels), len(si2.Labels))
	}
	for k, l1 := range si1.Labels {
		l2 := si2.Labels[k]
		if l1 != l2 {
			return fmt.Errorf("expected label %v, got %v", l1, l2)
		}
	}
	if si1.Created != si2.Created {
		return fmt.Errorf("expected Created %v, got %v", si1.Created, si2.Created)
	}
	if si1.Updated != si2.Updated {
		return fmt.Errorf("expected Updated %v, got %v", si1.Updated, si2.Updated)
	}

	return nil
}
