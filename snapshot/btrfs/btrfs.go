package btrfs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/docker/containerd"
	"github.com/docker/containerd/log"
	"github.com/docker/containerd/plugin"
	"github.com/docker/containerd/snapshot"
	"github.com/docker/containerd/snapshot/storage"
	"github.com/docker/containerd/snapshot/storage/boltdb"
	"github.com/pkg/errors"
	"github.com/stevvooe/go-btrfs"
)

type btrfsConfig struct {
	Device string `toml:"device"`
}

func init() {
	plugin.Register("snapshot-btrfs", &plugin.Registration{
		Type:   plugin.SnapshotPlugin,
		Config: &btrfsConfig{},
		Init: func(ic *plugin.InitContext) (interface{}, error) {
			root := filepath.Join(ic.Root, "snapshot", "btrfs")
			conf := ic.Config.(*btrfsConfig)
			if conf.Device == "" {
				// TODO: check device for root
				return nil, errors.Errorf("btrfs requires \"device\" configuration")
			}

			ms, err := boltdb.NewMetaStore(ic.Context, filepath.Join(root, "metadata.db"))
			if err != nil {
				return nil, err
			}

			return NewSnapshotter(conf.Device, root, ms)
		},
	})
}

type Snapshotter struct {
	device string // maybe we can resolve it with path?
	root   string // root provides paths for internal storage.
	ms     storage.MetaStore
}

func NewSnapshotter(device, root string, ms storage.MetaStore) (snapshot.Snapshotter, error) {
	var (
		active    = filepath.Join(root, "active")
		snapshots = filepath.Join(root, "snapshots")
	)

	for _, path := range []string{
		active,
		snapshots,
	} {
		if err := os.MkdirAll(path, 0755); err != nil {
			return nil, err
		}
	}

	return &Snapshotter{
		device: device,
		root:   root,
		ms:     ms,
	}, nil
}

// Stat returns the info for an active or committed snapshot by name or
// key.
//
// Should be used for parent resolution, existence checks and to discern
// the kind of snapshot.
func (b *Snapshotter) Stat(ctx context.Context, key string) (snapshot.Info, error) {
	ctx, t, err := b.ms.TransactionContext(ctx, false)
	if err != nil {
		return snapshot.Info{}, err
	}
	defer t.Rollback()
	return b.ms.Stat(ctx, key)
}

// Walk the committed snapshots.
func (b *Snapshotter) Walk(ctx context.Context, fn func(context.Context, snapshot.Info) error) error {
	ctx, t, err := b.ms.TransactionContext(ctx, false)
	if err != nil {
		return err
	}
	defer t.Rollback()
	return b.ms.Walk(ctx, fn)
}

func (b *Snapshotter) Prepare(ctx context.Context, key, parent string) ([]containerd.Mount, error) {
	return b.makeActive(ctx, key, parent, false)
}

func (b *Snapshotter) View(ctx context.Context, key, parent string) ([]containerd.Mount, error) {
	return b.makeActive(ctx, key, parent, true)
}

func (b *Snapshotter) makeActive(ctx context.Context, key, parent string, readonly bool) ([]containerd.Mount, error) {
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

	a, err := b.ms.CreateActive(ctx, key, parent, readonly)
	if err != nil {
		return nil, err
	}

	target := filepath.Join(b.root, "active", a.ID)

	if len(a.ParentIDs) == 0 {
		// create new subvolume
		// btrfs subvolume create /dir
		if err = btrfs.SubvolCreate(target); err != nil {
			return nil, err
		}
	} else {
		parentp := filepath.Join(b.root, "snapshots", a.ParentIDs[0])
		// btrfs subvolume snapshot /parent /subvol
		if err = btrfs.SubvolSnapshot(target, parentp, a.Readonly); err != nil {
			return nil, err
		}
	}
	err = t.Commit()
	t = nil
	if err != nil {
		return nil, err
	}

	return b.mounts(target)
}

func (b *Snapshotter) mounts(dir string) ([]containerd.Mount, error) {
	var options []string

	// get the subvolume id back out for the mount
	info, err := btrfs.SubvolInfo(dir)
	if err != nil {
		return nil, err
	}

	options = append(options, fmt.Sprintf("subvolid=%d", info.ID))

	if info.Readonly {
		options = append(options, "ro")
	}

	return []containerd.Mount{
		{
			Type:   "btrfs",
			Source: b.device, // device?
			// NOTE(stevvooe): While it would be nice to use to uuids for
			// mounts, they don't work reliably if the uuids are missing.
			Options: options,
		},
	}, nil
}

func (b *Snapshotter) Commit(ctx context.Context, name, key string) (err error) {
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

	id, err := b.ms.Commit(ctx, key, name)
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
			log.G(ctx).WithError(derr).Error("Failed to clean up new snapshot: %v", target)
		}
		return err
	}

	if derr := btrfs.SubvolDelete(source); derr != nil {
		log.G(ctx).WithError(derr).Warn("Failed to clean up active snapshot: %v", source)
	}

	return nil
}

// Mounts returns the mounts for the transaction identified by key. Can be
// called on an read-write or readonly transaction.
//
// This can be used to recover mounts after calling View or Prepare.
func (b *Snapshotter) Mounts(ctx context.Context, key string) ([]containerd.Mount, error) {
	ctx, t, err := b.ms.TransactionContext(ctx, false)
	if err != nil {
		return nil, err
	}
	a, err := b.ms.GetActive(ctx, key)
	t.Rollback()
	if err != nil {
		return nil, errors.Wrap(err, "failed to get active snapshot")
	}
	dir := filepath.Join(b.root, "active", a.ID)
	return b.mounts(dir)
}

// Remove abandons the transaction identified by key. All resources
// associated with the key will be removed.
func (b *Snapshotter) Remove(ctx context.Context, key string) (err error) {
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
				log.G(ctx).WithError(derr).Warn("Failed to clean up removed snapshot: %v", removed)
			}
		}
	}()

	id, k, err := b.ms.Remove(ctx, key)
	if err != nil {
		return errors.Wrap(err, "failed to remove snapshot")
	}

	if k == snapshot.KindActive {
		source = filepath.Join(b.root, "active", id)

		info, err := btrfs.SubvolInfo(source)
		if err != nil {
			source = ""
			return err
		}

		readonly = info.Readonly
		removed = filepath.Join(b.root, "active", "rm-"+id)
	} else {
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
			// Keep removed to allow for manual restore
			removed = ""
			log.G(ctx).WithError(err1).Error("Failed to restore source snapshot %v from %v", source, removed)
		}
		return err
	}

	return nil
}
