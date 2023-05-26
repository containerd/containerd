//go:build linux || freebsd

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

package zfs

import (
	"context"
	"fmt"
	"math"
	"path/filepath"

	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/snapshots"
	"github.com/containerd/containerd/snapshots/storage"
	"github.com/mistifyio/go-zfs/v3"
)

const (
	// snapshotSuffix is used as follows:
	//	active := filepath.Join(dataset.Name, id)
	//      committed := active + "@" + snapshotSuffix
	snapshotSuffix = "snapshot"

	// Using this typed MaxInt64 to prevent integer overlow on 32bit
	maxSnapshotSize int64 = math.MaxInt64
)

type snapshotter struct {
	dataset *zfs.Dataset
	ms      *storage.MetaStore
}

// NewSnapshotter returns a Snapshotter using zfs. Uses the provided
// root directory for snapshots and stores the metadata in
// a file in the provided root.
// root needs to be a mount point of zfs.
func NewSnapshotter(root string) (snapshots.Snapshotter, error) {
	m, err := mount.Lookup(root)
	if err != nil {
		return nil, err
	}
	if m.FSType != "zfs" {
		return nil, fmt.Errorf("path %s must be a zfs filesystem to be used with the zfs snapshotter", root)
	}
	dataset, err := zfs.GetDataset(m.Source)
	if err != nil {
		return nil, err
	}

	ms, err := storage.NewMetaStore(filepath.Join(root, "metadata.db"))
	if err != nil {
		return nil, err
	}

	b := &snapshotter{
		dataset: dataset,
		ms:      ms,
	}
	return b, nil
}

var zfsCreateProperties = map[string]string{
	"mountpoint": "legacy",
}

// createFilesystem creates but not mount.
func createFilesystem(datasetName string) (*zfs.Dataset, error) {
	return zfs.CreateFilesystem(datasetName, zfsCreateProperties)
}

// cloneFilesystem clones but not mount.
func cloneFilesystem(datasetName string, snapshot *zfs.Dataset) (*zfs.Dataset, error) {
	return snapshot.Clone(datasetName, zfsCreateProperties)
}

func destroy(dataset *zfs.Dataset) error {
	return dataset.Destroy(zfs.DestroyDefault)
}

func destroySnapshot(dataset *zfs.Dataset) error {
	return dataset.Destroy(zfs.DestroyDeferDeletion)
}

// Stat returns the info for an active or committed snapshot by name or
// key.
//
// Should be used for parent resolution, existence checks and to discern
// the kind of snapshot.
func (z *snapshotter) Stat(ctx context.Context, key string) (snapshots.Info, error) {
	ctx, t, err := z.ms.TransactionContext(ctx, false)
	if err != nil {
		return snapshots.Info{}, err
	}
	defer t.Rollback() //nolint:errcheck
	_, info, _, err := storage.GetInfo(ctx, key)
	if err != nil {
		return snapshots.Info{}, err
	}

	return info, nil
}

// Usage retrieves the disk usage of the top-level snapshot.
func (z *snapshotter) Usage(ctx context.Context, key string) (snapshots.Usage, error) {
	return z.usage(ctx, key)
}

func (z *snapshotter) usage(ctx context.Context, key string) (snapshots.Usage, error) {
	ctx, t, err := z.ms.TransactionContext(ctx, false)
	if err != nil {
		return snapshots.Usage{}, err
	}
	id, info, usage, err := storage.GetInfo(ctx, key)
	t.Rollback() //nolint:errcheck

	if err != nil {
		return snapshots.Usage{}, err
	}

	if info.Kind == snapshots.KindActive {
		activeName := filepath.Join(z.dataset.Name, id)
		sDataset, err := zfs.GetDataset(activeName)
		if err != nil {
			return snapshots.Usage{}, err
		}

		if int64(sDataset.Used) > maxSnapshotSize {
			return snapshots.Usage{}, fmt.Errorf("Dataset size exceeds maximum snapshot size of %d bytes", maxSnapshotSize)
		}

		usage = snapshots.Usage{
			Size:   int64(sDataset.Used),
			Inodes: -1,
		}
	}

	return usage, nil
}

// Walk the committed snapshots.
func (z *snapshotter) Walk(ctx context.Context, fn snapshots.WalkFunc, filters ...string) error {
	ctx, t, err := z.ms.TransactionContext(ctx, false)
	if err != nil {
		return err
	}
	defer t.Rollback() //nolint:errcheck
	return storage.WalkInfo(ctx, fn, filters...)
}

func (z *snapshotter) Prepare(ctx context.Context, key, parent string, opts ...snapshots.Opt) ([]mount.Mount, error) {
	return z.createSnapshot(ctx, snapshots.KindActive, key, parent, opts...)
}

func (z *snapshotter) View(ctx context.Context, key, parent string, opts ...snapshots.Opt) ([]mount.Mount, error) {
	return z.createSnapshot(ctx, snapshots.KindView, key, parent, opts...)
}

func (z *snapshotter) createSnapshot(ctx context.Context, kind snapshots.Kind, key, parent string, opts ...snapshots.Opt) ([]mount.Mount, error) {
	ctx, t, err := z.ms.TransactionContext(ctx, true)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil && t != nil {
			if rerr := t.Rollback(); rerr != nil {
				log.G(ctx).WithError(rerr).Warn("Failure rolling back transaction")
			}
		}
	}()

	a, err := storage.CreateSnapshot(ctx, kind, key, parent, opts...)
	if err != nil {
		return nil, err
	}

	targetName := filepath.Join(z.dataset.Name, a.ID)
	var target *zfs.Dataset
	if len(a.ParentIDs) == 0 {
		target, err = createFilesystem(targetName)
		if err != nil {
			return nil, err
		}
	} else {
		parent0Name := filepath.Join(z.dataset.Name, a.ParentIDs[0]) + "@" + snapshotSuffix
		parent0, err := zfs.GetDataset(parent0Name)
		if err != nil {
			return nil, err
		}
		target, err = cloneFilesystem(targetName, parent0)
		if err != nil {
			return nil, err
		}
	}

	err = t.Commit()
	t = nil
	if err != nil {
		if derr := destroy(target); derr != nil {
			log.G(ctx).WithError(derr).WithField("targetName", targetName).Error("failed to delete dataset")
		}
		return nil, err
	}
	readonly := kind == snapshots.KindView
	return z.mounts(target, readonly)
}

func (z *snapshotter) mounts(dataset *zfs.Dataset, readonly bool) ([]mount.Mount, error) {
	var options []string
	if readonly {
		options = append(options, "ro")
	}
	return []mount.Mount{
		{
			Type:    "zfs",
			Source:  dataset.Name,
			Options: options,
		},
	}, nil
}

func (z *snapshotter) Commit(ctx context.Context, name, key string, opts ...snapshots.Opt) (err error) {
	usage, err := z.usage(ctx, key)
	if err != nil {
		return fmt.Errorf("failed to compute usage: %w", err)
	}

	ctx, t, err := z.ms.TransactionContext(ctx, true)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil && t != nil {
			if rerr := t.Rollback(); rerr != nil {
				log.G(ctx).WithError(rerr).Warn("Failure rolling back transaction")
			}
		}
	}()

	id, err := storage.CommitActive(ctx, key, name, usage, opts...)
	if err != nil {
		return fmt.Errorf("failed to commit: %w", err)
	}

	activeName := filepath.Join(z.dataset.Name, id)
	active, err := zfs.GetDataset(activeName)
	if err != nil {
		return err
	}
	snapshot, err := active.Snapshot(snapshotSuffix, false)
	if err != nil {
		return err
	}
	err = t.Commit()
	t = nil
	if err != nil {
		snapshotName := activeName + "@" + snapshotSuffix
		if derr := destroySnapshot(snapshot); derr != nil {
			log.G(ctx).WithError(derr).WithField("snapshotName", snapshotName).Error("failed to delete dataset")
		}
		return err
	}
	return nil
}

// Mounts returns the mounts for the transaction identified by key. Can be
// called on an read-write or readonly transaction.
//
// This can be used to recover mounts after calling View or Prepare.
func (z *snapshotter) Mounts(ctx context.Context, key string) ([]mount.Mount, error) {
	ctx, t, err := z.ms.TransactionContext(ctx, false)
	if err != nil {
		return nil, err
	}
	s, err := storage.GetSnapshot(ctx, key)
	t.Rollback() //nolint:errcheck
	if err != nil {
		return nil, fmt.Errorf("failed to get active snapshot: %w", err)
	}
	sName := filepath.Join(z.dataset.Name, s.ID)
	sDataset, err := zfs.GetDataset(sName)
	if err != nil {
		return nil, err
	}
	return z.mounts(sDataset, false)
}

// Remove abandons the transaction identified by key. All resources
// associated with the key will be removed.
func (z *snapshotter) Remove(ctx context.Context, key string) (err error) {
	ctx, t, err := z.ms.TransactionContext(ctx, true)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil && t != nil {
			if rerr := t.Rollback(); rerr != nil {
				log.G(ctx).WithError(rerr).Warn("Failure rolling back transaction")
			}
		}
		// FIXME: rolling back the removed ZFS dataset is unsupported yet
	}()

	id, k, err := storage.Remove(ctx, key)
	if err != nil {
		return fmt.Errorf("failed to remove snapshot: %w", err)
	}

	datasetName := filepath.Join(z.dataset.Name, id)
	if k == snapshots.KindCommitted {
		snapshotName := datasetName + "@" + snapshotSuffix
		snapshot, err := zfs.GetDataset(snapshotName)
		if err != nil {
			return err
		}
		if err = destroySnapshot(snapshot); err != nil {
			return err
		}
	}
	dataset, err := zfs.GetDataset(datasetName)
	if err != nil {
		return err
	}
	if err = destroy(dataset); err != nil {
		return err
	}
	err = t.Commit()
	t = nil
	return err
}

func (o *snapshotter) Update(ctx context.Context, info snapshots.Info, fieldpaths ...string) (snapshots.Info, error) {
	ctx, t, err := o.ms.TransactionContext(ctx, true)
	if err != nil {
		return snapshots.Info{}, err
	}

	info, err = storage.UpdateInfo(ctx, info, fieldpaths...)
	if err != nil {
		t.Rollback() //nolint:errcheck
		return snapshots.Info{}, err
	}

	if err := t.Commit(); err != nil {
		return snapshots.Info{}, err
	}

	return info, nil
}

func (o *snapshotter) Close() error {
	return o.ms.Close()
}
