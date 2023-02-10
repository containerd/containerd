//go:build linux && !no_btrfs && cgo

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

package btrfs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/containerd/btrfs/v2"
	"github.com/containerd/continuity/fs"

	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/snapshots"
	"github.com/containerd/containerd/snapshots/storage"

	"github.com/sirupsen/logrus"
)

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
	if st, err := os.Stat(root); err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
		if err := os.Mkdir(root, 0700); err != nil {
			return nil, err
		}
	} else if st.Mode()&os.ModePerm != 0700 {
		if err := os.Chmod(root, 0700); err != nil {
			return nil, err
		}
	}

	mnt, err := mount.Lookup(root)
	if err != nil {
		return nil, err
	}
	if mnt.FSType != "btrfs" {
		return nil, fmt.Errorf("path %s (%s) must be a btrfs filesystem to be used with the btrfs snapshotter: %w", root, mnt.FSType, plugin.ErrSkipPlugin)
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
func (b *snapshotter) Stat(ctx context.Context, key string) (info snapshots.Info, err error) {
	err = b.ms.WithTransaction(ctx, false, func(ctx context.Context) error {
		_, info, _, err = storage.GetInfo(ctx, key)
		return err
	})

	if err != nil {
		return snapshots.Info{}, err
	}

	return info, nil
}

func (b *snapshotter) Update(ctx context.Context, info snapshots.Info, fieldpaths ...string) (_ snapshots.Info, err error) {
	err = b.ms.WithTransaction(ctx, true, func(ctx context.Context) error {
		info, err = storage.UpdateInfo(ctx, info, fieldpaths...)
		return err
	})

	if err != nil {
		return snapshots.Info{}, err
	}

	return info, nil
}

// Usage retrieves the disk usage of the top-level snapshot.
func (b *snapshotter) Usage(ctx context.Context, key string) (snapshots.Usage, error) {
	return b.usage(ctx, key)
}

func (b *snapshotter) usage(ctx context.Context, key string) (usage snapshots.Usage, err error) {
	var (
		id, parentID string
		info         snapshots.Info
	)

	err = b.ms.WithTransaction(ctx, false, func(ctx context.Context) error {
		id, info, usage, err = storage.GetInfo(ctx, key)

		if err == nil && info.Kind == snapshots.KindActive && info.Parent != "" {
			parentID, _, _, err = storage.GetInfo(ctx, info.Parent)
		}
		return err
	})

	if err != nil {
		return snapshots.Usage{}, err
	}

	if info.Kind == snapshots.KindActive {
		var du fs.Usage
		p := filepath.Join(b.root, "active", id)
		if parentID != "" {
			du, err = fs.DiffUsage(ctx, filepath.Join(b.root, "snapshots", parentID), p)
		} else {
			du, err = fs.DiskUsage(ctx, p)
		}
		if err != nil {
			// TODO(stevvooe): Consider not reporting an error in this case.
			return snapshots.Usage{}, err
		}

		usage = snapshots.Usage(du)
	}

	return usage, nil
}

// Walk the committed snapshots.
func (b *snapshotter) Walk(ctx context.Context, fn snapshots.WalkFunc, fs ...string) (err error) {
	return b.ms.WithTransaction(ctx, false, func(ctx context.Context) error {
		return storage.WalkInfo(ctx, fn, fs...)
	})
}

func (b *snapshotter) Prepare(ctx context.Context, key, parent string, opts ...snapshots.Opt) ([]mount.Mount, error) {
	return b.makeSnapshot(ctx, snapshots.KindActive, key, parent, opts)
}

func (b *snapshotter) View(ctx context.Context, key, parent string, opts ...snapshots.Opt) ([]mount.Mount, error) {
	return b.makeSnapshot(ctx, snapshots.KindView, key, parent, opts)
}

func (b *snapshotter) makeSnapshot(ctx context.Context, kind snapshots.Kind, key, parent string, opts []snapshots.Opt) (_ []mount.Mount, err error) {
	var (
		target string
		s      storage.Snapshot
	)

	err = b.ms.WithTransaction(ctx, true, func(ctx context.Context) error {
		s, err = storage.CreateSnapshot(ctx, kind, key, parent, opts...)
		if err != nil {
			return err
		}

		target = filepath.Join(b.root, strings.ToLower(s.Kind.String()), s.ID)

		if len(s.ParentIDs) == 0 {
			// create new subvolume
			// btrfs subvolume create /dir
			return btrfs.SubvolCreate(target)
		}
		parentp := filepath.Join(b.root, "snapshots", s.ParentIDs[0])

		// btrfs subvolume snapshot /parent /subvol
		readOnly := kind == snapshots.KindView
		return btrfs.SubvolSnapshot(target, parentp, readOnly)
	})

	if err != nil {
		if target != "" {
			if derr := btrfs.SubvolDelete(target); derr != nil {
				log.G(ctx).WithError(derr).WithField("subvolume", target).Error("failed to delete subvolume")
			}
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
	var usage snapshots.Usage
	usage, err = b.usage(ctx, key)
	if err != nil {
		return fmt.Errorf("failed to compute usage: %w", err)
	}

	var source, target string
	err = b.ms.WithTransaction(ctx, true, func(ctx context.Context) error {
		id, err := storage.CommitActive(ctx, key, name, usage, opts...) // TODO(stevvooe): Resolve a usage value for btrfs
		if err != nil {
			return fmt.Errorf("failed to commit: %w", err)
		}

		source = filepath.Join(b.root, "active", id)
		target = filepath.Join(b.root, "snapshots", id)

		return btrfs.SubvolSnapshot(target, source, true)
	})

	if err != nil {
		if target != "" {
			if derr := btrfs.SubvolDelete(target); derr != nil {
				log.G(ctx).WithError(derr).WithField("subvolume", target).Error("failed to delete subvolume")
			}
		}

		return err
	}

	if source != "" {
		if derr := btrfs.SubvolDelete(source); derr != nil {
			// Log as warning, only needed for cleanup, will not cause name collision
			log.G(ctx).WithError(derr).WithField("subvolume", source).Warn("failed to delete subvolume")
		}
	}

	return nil
}

// Mounts returns the mounts for the transaction identified by key. Can be
// called on an read-write or readonly transaction.
//
// This can be used to recover mounts after calling View or Prepare.
func (b *snapshotter) Mounts(ctx context.Context, key string) (_ []mount.Mount, err error) {
	var s storage.Snapshot

	err = b.ms.WithTransaction(ctx, false, func(ctx context.Context) error {
		s, err = storage.GetSnapshot(ctx, key)
		return err
	})

	if err != nil {
		return nil, fmt.Errorf("failed to get active snapshot: %w", err)
	}

	dir := filepath.Join(b.root, strings.ToLower(s.Kind.String()), s.ID)
	return b.mounts(dir, s)
}

// Remove abandons the transaction identified by key. All resources
// associated with the key will be removed.
func (b *snapshotter) Remove(ctx context.Context, key string) (err error) {
	var (
		source, removed   string
		readonly, restore bool
	)

	defer func() {
		if removed != "" {
			if derr := btrfs.SubvolDelete(removed); derr != nil {
				log.G(ctx).WithError(derr).WithField("subvolume", removed).Warn("failed to delete subvolume")
			}
		}
	}()

	err = b.ms.WithTransaction(ctx, true, func(ctx context.Context) error {
		id, k, err := storage.Remove(ctx, key)
		if err != nil {
			return fmt.Errorf("failed to remove snapshot: %w", err)
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

		if err = btrfs.SubvolSnapshot(removed, source, readonly); err != nil {
			removed = ""
			return err
		}

		if err = btrfs.SubvolDelete(source); err != nil {
			return fmt.Errorf("failed to remove snapshot %v: %w", source, err)
		}

		restore = true
		return nil
	})

	if err != nil {
		if restore { // means failed to commit transaction
			// Attempt to restore source
			if err1 := btrfs.SubvolSnapshot(source, removed, readonly); err1 != nil {
				log.G(ctx).WithFields(logrus.Fields{
					logrus.ErrorKey: err1,
					"subvolume":     source,
					"renamed":       removed,
				}).Error("failed to restore subvolume from renamed")
				// Keep removed to allow for manual restore
				removed = ""
			}
		}

		return err
	}

	return nil
}

// Close closes the snapshotter
func (b *snapshotter) Close() error {
	return b.ms.Close()
}
