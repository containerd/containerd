// +build linux,!no_btrfs

package btrfs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/containerd/btrfs"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/platforms"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/snapshots"
	"github.com/containerd/containerd/snapshots/storage"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

func init() {
	plugin.Register(&plugin.Registration{
		ID:   "btrfs",
		Type: plugin.SnapshotPlugin,
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			ic.Meta.Platforms = []ocispec.Platform{platforms.DefaultSpec()}
			ic.Meta.Exports = map[string]string{"root": ic.Root}
			return NewSnapshotter(ic.Root)
		},
	})
}

type snapshotter struct {
	device string // device of the root
	root   string // root provides paths for internal storage.
	ms     *storage.MetaStore
}

// NewSnapshotter returns a Snapshotter using btrfs. Uses the provided
// root directory for snapshots and stores the metadata in
// a file in the provided root.
// root needs to be a mount point of btrfs.
func NewSnapshotter(root string) (snapshots.Snapshotter, error) {
	// If directory does not exist, create it
	if _, err := os.Stat(root); err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
		if err := os.Mkdir(root, 0755); err != nil {
			return nil, err
		}
	}

	mnt, err := mount.Lookup(root)
	if err != nil {
		return nil, err
	}
	if mnt.FSType != "btrfs" {
		return nil, fmt.Errorf("path %s must be a btrfs filesystem to be used with the btrfs snapshotter", root)
	}
	var (
		active    = filepath.Join(root, "active")
		view      = filepath.Join(root, "view")
		snapshots = filepath.Join(root, "snapshots")
	)

	for _, path := range []string{
		active,
		view,
		snapshots,
	} {
		if err := os.Mkdir(path, 0755); err != nil && !os.IsExist(err) {
			return nil, err
		}
	}
	ms, err := storage.NewMetaStore(filepath.Join(root, "metadata.db"))
	if err != nil {
		return nil, err
	}

	return &snapshotter{
		device: mnt.Source,
		root:   root,
		ms:     ms,
	}, nil
}

// Stat returns the info for an active or committed snapshot by name or
// key.
//
// Should be used for parent resolution, existence checks and to discern
// the kind of snapshot.
func (b *snapshotter) Stat(ctx context.Context, key string) (snapshots.Info, error) {
	ctx, t, err := b.ms.TransactionContext(ctx, false)
	if err != nil {
		return snapshots.Info{}, err
	}
	defer t.Rollback()
	_, info, _, err := storage.GetInfo(ctx, key)
	if err != nil {
		return snapshots.Info{}, err
	}

	return info, nil
}

func (b *snapshotter) Update(ctx context.Context, info snapshots.Info, fieldpaths ...string) (snapshots.Info, error) {
	ctx, t, err := b.ms.TransactionContext(ctx, true)
	if err != nil {
		return snapshots.Info{}, err
	}

	info, err = storage.UpdateInfo(ctx, info, fieldpaths...)
	if err != nil {
		t.Rollback()
		return snapshots.Info{}, err
	}

	if err := t.Commit(); err != nil {
		return snapshots.Info{}, err
	}

	return info, nil
}

// Usage retrieves the disk usage of the top-level snapshot.
//
// For active snapshot, the `excl` value of L0 QGroup is used, and committed on Commit.
// For view snapshot, always 0 is returned.
// For committed snapshot, the committed value is returned.
func (b *snapshotter) Usage(ctx context.Context, key string) (snapshots.Usage, error) {
	ctx, t, err := b.ms.TransactionContext(ctx, false)
	if err != nil {
		return snapshots.Usage{}, err
	}
	defer t.Rollback()
	id, info, committedUsage, err := storage.GetInfo(ctx, key)
	if err != nil {
		return snapshots.Usage{}, err
	}
	switch info.Kind {
	case snapshots.KindActive:
		return b.activeUsage(ctx, id)
	case snapshots.KindView:
		return snapshots.Usage{Size: 0, Inodes: 0}, nil
	default:
		return committedUsage, nil
	}

}

// activeUsage returns the `excl` value of L0 QGroup is used, and committed on Commit.
func (b *snapshotter) activeUsage(ctx context.Context, activeSnapshotID string) (snapshots.Usage, error) {
	target := filepath.Join(b.root, "active", activeSnapshotID)
	subvolID, err := btrfs.SubvolID(target)
	if err != nil {
		return snapshots.Usage{}, err
	}
	if err := btrfs.Sync(target); err != nil {
		return snapshots.Usage{}, err
	}
	qgItems, err := btrfs.QGroupInfo(target)
	if err != nil {
		return snapshots.Usage{}, errors.Wrapf(err, "could not query %q. quota not enabled?", target)
	}
	for _, qgItem := range qgItems {
		// http://sensille.com/qgroups.pdf
		// Qgroups of level 0 get created automatically when a subvolume/snapshot gets created.
		// The ID of the qgroup corresponds to the ID of the subvolume.
		if qgItem.QGroupIDHigh == 0 && qgItem.QGroupIDLow == subvolID {
			return snapshots.Usage{
				Size:   int64(qgItem.Exclusive), // not ExclusiveCompressed,
				Inodes: -1,
			}, nil
		}
	}
	return snapshots.Usage{}, errors.Errorf("no qgroup found for %q (ID %d)", target, subvolID)
}

// Walk the committed snapshots.
func (b *snapshotter) Walk(ctx context.Context, fn func(context.Context, snapshots.Info) error) error {
	ctx, t, err := b.ms.TransactionContext(ctx, false)
	if err != nil {
		return err
	}
	defer t.Rollback()
	return storage.WalkInfo(ctx, fn)
}

func (b *snapshotter) Prepare(ctx context.Context, key, parent string, opts ...snapshots.Opt) ([]mount.Mount, error) {
	return b.makeSnapshot(ctx, snapshots.KindActive, key, parent, opts)
}

func (b *snapshotter) View(ctx context.Context, key, parent string, opts ...snapshots.Opt) ([]mount.Mount, error) {
	return b.makeSnapshot(ctx, snapshots.KindView, key, parent, opts)
}

func (b *snapshotter) makeSnapshot(ctx context.Context, kind snapshots.Kind, key, parent string, opts []snapshots.Opt) ([]mount.Mount, error) {
	ctx, t, err := b.ms.TransactionContext(ctx, true)
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

	s, err := storage.CreateSnapshot(ctx, kind, key, parent, opts...)
	if err != nil {
		return nil, err
	}

	target := filepath.Join(b.root, strings.ToLower(s.Kind.String()), s.ID)

	if len(s.ParentIDs) == 0 {
		// create new subvolume
		// btrfs subvolume create /dir
		if err = btrfs.SubvolCreate(target); err != nil {
			return nil, err
		}
	} else {
		parentp := filepath.Join(b.root, "snapshots", s.ParentIDs[0])

		var readonly bool
		if kind == snapshots.KindView {
			readonly = true
		}

		// btrfs subvolume snapshot /parent /subvol
		if err = btrfs.SubvolSnapshot(target, parentp, readonly); err != nil {
			return nil, err
		}
	}
	if err = btrfs.QuotaCtl(target, true); err != nil {
		return nil, errors.Wrapf(err, "could not enable quota for %q", target)
	}
	err = t.Commit()
	t = nil
	if err != nil {
		if derr := btrfs.SubvolDelete(target); derr != nil {
			log.G(ctx).WithError(derr).WithField("subvolume", target).Error("Failed to delete subvolume")
		}
		return nil, err
	}

	return b.mounts(target, s)
}

func (b *snapshotter) mounts(dir string, s storage.Snapshot) ([]mount.Mount, error) {
	var options []string

	// get the subvolume id back out for the mount
	sid, err := btrfs.SubvolID(dir)
	if err != nil {
		return nil, err
	}

	options = append(options, fmt.Sprintf("subvolid=%d", sid))

	if s.Kind != snapshots.KindActive {
		options = append(options, "ro")
	}

	return []mount.Mount{
		{
			Type:   "btrfs",
			Source: b.device,
			// NOTE(stevvooe): While it would be nice to use to uuids for
			// mounts, they don't work reliably if the uuids are missing.
			Options: options,
		},
	}, nil
}

func (b *snapshotter) Commit(ctx context.Context, name, key string, opts ...snapshots.Opt) (err error) {
	ctx, t, err := b.ms.TransactionContext(ctx, true)
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

	active, err := storage.GetSnapshot(ctx, key)
	if err != nil {
		return err
	}
	activeUsage, err := b.activeUsage(ctx, active.ID)
	if err != nil {
		return errors.Wrapf(err, "could not get usage for active %q (ID %s)", key, active.ID)
	}
	id, err := storage.CommitActive(ctx, key, name, activeUsage, opts...)
	if err != nil {
		return errors.Wrap(err, "failed to commit")
	}

	source := filepath.Join(b.root, "active", id)
	target := filepath.Join(b.root, "snapshots", id)

	if err := btrfs.SubvolSnapshot(target, source, true); err != nil {
		return err
	}

	err = t.Commit()
	t = nil
	if err != nil {
		if derr := btrfs.SubvolDelete(target); derr != nil {
			log.G(ctx).WithError(derr).WithField("subvolume", target).Error("Failed to delete subvolume")
		}
		return err
	}

	if derr := btrfs.SubvolDelete(source); derr != nil {
		// Log as warning, only needed for cleanup, will not cause name collision
		log.G(ctx).WithError(derr).WithField("subvolume", source).Warn("Failed to delete subvolume")
	}

	return nil
}

// Mounts returns the mounts for the transaction identified by key. Can be
// called on an read-write or readonly transaction.
//
// This can be used to recover mounts after calling View or Prepare.
func (b *snapshotter) Mounts(ctx context.Context, key string) ([]mount.Mount, error) {
	ctx, t, err := b.ms.TransactionContext(ctx, false)
	if err != nil {
		return nil, err
	}
	s, err := storage.GetSnapshot(ctx, key)
	t.Rollback()
	if err != nil {
		return nil, errors.Wrap(err, "failed to get active snapshot")
	}

	dir := filepath.Join(b.root, strings.ToLower(s.Kind.String()), s.ID)
	return b.mounts(dir, s)
}

// Remove abandons the transaction identified by key. All resources
// associated with the key will be removed.
func (b *snapshotter) Remove(ctx context.Context, key string) (err error) {
	var (
		source, removed string
		readonly        bool
	)

	ctx, t, err := b.ms.TransactionContext(ctx, true)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil && t != nil {
			if rerr := t.Rollback(); rerr != nil {
				log.G(ctx).WithError(rerr).Warn("Failure rolling back transaction")
			}
		}

		if removed != "" {
			if derr := btrfs.SubvolDelete(removed); derr != nil {
				log.G(ctx).WithError(derr).WithField("subvolume", removed).Warn("Failed to delete subvolume")
			}
		}
	}()

	id, k, err := storage.Remove(ctx, key)
	if err != nil {
		return errors.Wrap(err, "failed to remove snapshot")
	}

	switch k {
	case snapshots.KindView:
		source = filepath.Join(b.root, "view", id)
		removed = filepath.Join(b.root, "view", "rm-"+id)
		readonly = true
	case snapshots.KindActive:
		source = filepath.Join(b.root, "active", id)
		removed = filepath.Join(b.root, "active", "rm-"+id)
	case snapshots.KindCommitted:
		source = filepath.Join(b.root, "snapshots", id)
		removed = filepath.Join(b.root, "snapshots", "rm-"+id)
		readonly = true
	}

	if err := btrfs.SubvolSnapshot(removed, source, readonly); err != nil {
		removed = ""
		return err
	}

	if err := btrfs.SubvolDelete(source); err != nil {
		return errors.Wrapf(err, "failed to remove snapshot %v", source)
	}

	err = t.Commit()
	t = nil
	if err != nil {
		// Attempt to restore source
		if err1 := btrfs.SubvolSnapshot(source, removed, readonly); err1 != nil {
			log.G(ctx).WithFields(logrus.Fields{
				logrus.ErrorKey: err1,
				"subvolume":     source,
				"renamed":       removed,
			}).Error("Failed to restore subvolume from renamed")
			// Keep removed to allow for manual restore
			removed = ""
		}
		return err
	}

	return nil
}

// Close closes the snapshotter
func (b *snapshotter) Close() error {
	return b.ms.Close()
}
