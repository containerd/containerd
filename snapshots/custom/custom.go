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

package custom

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"

	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/snapshots"
	"github.com/containerd/containerd/snapshots/storage"

	"github.com/containerd/continuity/fs"
)

type snapshotter struct {
	root string
	ms   *storage.MetaStore
}

// NewSnapshotter returns a Snapshotter backed by tmpfs mounts with
// huge=advise and noatime flags. Metadata is stored on disk under root
// but snapshot content lives entirely in tmpfs.
func NewSnapshotter(root string) (snapshots.Snapshotter, error) {
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	ms, err := storage.NewMetaStore(filepath.Join(root, "metadata.db"))
	if err != nil {
		return nil, err
	}

	if err := os.Mkdir(filepath.Join(root, "snapshots"), 0700); err != nil && !os.IsExist(err) {
		return nil, err
	}

	return &snapshotter{
		root: root,
		ms:   ms,
	}, nil
}

func (o *snapshotter) Stat(ctx context.Context, key string) (info snapshots.Info, err error) {
	err = o.ms.WithTransaction(ctx, false, func(ctx context.Context) error {
		_, info, _, err = storage.GetInfo(ctx, key)
		return err
	})
	if err != nil {
		return snapshots.Info{}, err
	}
	return info, nil
}

func (o *snapshotter) Update(ctx context.Context, info snapshots.Info, fieldpaths ...string) (_ snapshots.Info, err error) {
	err = o.ms.WithTransaction(ctx, true, func(ctx context.Context) error {
		info, err = storage.UpdateInfo(ctx, info, fieldpaths...)
		return err
	})
	if err != nil {
		return snapshots.Info{}, err
	}
	return info, nil
}

func (o *snapshotter) Usage(ctx context.Context, key string) (usage snapshots.Usage, err error) {
	var (
		id   string
		info snapshots.Info
	)

	err = o.ms.WithTransaction(ctx, false, func(ctx context.Context) error {
		id, info, usage, err = storage.GetInfo(ctx, key)
		return err
	})
	if err != nil {
		return snapshots.Usage{}, err
	}

	if info.Kind == snapshots.KindActive {
		du, err := fs.DiskUsage(ctx, o.getSnapshotDir(id))
		if err != nil {
			return snapshots.Usage{}, err
		}
		usage = snapshots.Usage(du)
	}

	return usage, nil
}

func (o *snapshotter) Prepare(ctx context.Context, key, parent string, opts ...snapshots.Opt) ([]mount.Mount, error) {
	return o.createSnapshot(ctx, snapshots.KindActive, key, parent, opts)
}

func (o *snapshotter) View(ctx context.Context, key, parent string, opts ...snapshots.Opt) ([]mount.Mount, error) {
	return o.createSnapshot(ctx, snapshots.KindView, key, parent, opts)
}

func (o *snapshotter) Mounts(ctx context.Context, key string) (_ []mount.Mount, err error) {
	var s storage.Snapshot
	err = o.ms.WithTransaction(ctx, false, func(ctx context.Context) error {
		s, err = storage.GetSnapshot(ctx, key)
		if err != nil {
			return fmt.Errorf("failed to get snapshot mount: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return o.mounts(s), nil
}

func (o *snapshotter) Commit(ctx context.Context, name, key string, opts ...snapshots.Opt) error {
	return o.ms.WithTransaction(ctx, true, func(ctx context.Context) error {
		id, _, _, err := storage.GetInfo(ctx, key)
		if err != nil {
			return err
		}

		usage, err := fs.DiskUsage(ctx, o.getSnapshotDir(id))
		if err != nil {
			return err
		}

		if _, err = storage.CommitActive(ctx, key, name, snapshots.Usage(usage), opts...); err != nil {
			return fmt.Errorf("failed to commit snapshot: %w", err)
		}
		return nil
	})
}

func (o *snapshotter) Remove(ctx context.Context, key string) (err error) {
	var (
		renamed, path string
		restore       bool
	)

	err = o.ms.WithTransaction(ctx, true, func(ctx context.Context) error {
		id, _, err := storage.Remove(ctx, key)
		if err != nil {
			return fmt.Errorf("failed to remove: %w", err)
		}

		path = o.getSnapshotDir(id)
		renamed = filepath.Join(o.root, "snapshots", "rm-"+id)
		if err = os.Rename(path, renamed); err != nil {
			if !os.IsNotExist(err) {
				return fmt.Errorf("failed to rename: %w", err)
			}
			renamed = ""
		}

		restore = true
		return nil
	})

	if err != nil {
		if renamed != "" && restore {
			if err1 := os.Rename(renamed, path); err1 != nil {
				log.G(ctx).WithError(err1).WithField("path", renamed).Error("failed to rename after failed commit")
			}
		}
		return err
	}

	if renamed != "" {
		// Unmount the tmpfs before removing the directory.
		_ = unix.Unmount(renamed, unix.MNT_DETACH)
		if err := os.RemoveAll(renamed); err != nil {
			log.G(ctx).WithError(err).WithField("path", renamed).Warnf("failed to remove root filesystem")
		}
	}

	return nil
}

func (o *snapshotter) Walk(ctx context.Context, fn snapshots.WalkFunc, fs ...string) error {
	return o.ms.WithTransaction(ctx, false, func(ctx context.Context) error {
		return storage.WalkInfo(ctx, fn, fs...)
	})
}

func (o *snapshotter) createSnapshot(ctx context.Context, kind snapshots.Kind, key, parent string, opts []snapshots.Opt) (_ []mount.Mount, err error) {
	var (
		path, td string
		s        storage.Snapshot
	)

	if kind == snapshots.KindActive || parent == "" {
		td, err = os.MkdirTemp(filepath.Join(o.root, "snapshots"), "new-")
		if err != nil {
			return nil, fmt.Errorf("failed to create temp dir: %w", err)
		}
		if err := os.Chmod(td, 0755); err != nil {
			return nil, fmt.Errorf("failed to chmod %s to 0755: %w", td, err)
		}
		defer func() {
			if err != nil {
				if td != "" {
					_ = unix.Unmount(td, unix.MNT_DETACH)
					if err1 := os.RemoveAll(td); err1 != nil {
						err = fmt.Errorf("remove failed: %v: %w", err1, err)
					}
				}
				if path != "" {
					_ = unix.Unmount(path, unix.MNT_DETACH)
					if err1 := os.RemoveAll(path); err1 != nil {
						err = fmt.Errorf("failed to remove path: %v: %w", err1, err)
					}
				}
			}
		}()

		// Mount tmpfs with huge=advise and noatime on the snapshot directory.
		if err = unix.Mount("tmpfs", td, "tmpfs", unix.MS_NOATIME, "huge=advise"); err != nil {
			return nil, fmt.Errorf("failed to mount tmpfs on %s: %w", td, err)
		}
	}

	err = o.ms.WithTransaction(ctx, true, func(ctx context.Context) error {
		s, err = storage.CreateSnapshot(ctx, kind, key, parent, opts...)
		if err != nil {
			return fmt.Errorf("failed to create snapshot: %w", err)
		}

		if td != "" {
			if len(s.ParentIDs) > 0 {
				parentDir := o.getSnapshotDir(s.ParentIDs[0])
				xattrErrorHandler := func(dst, src, xattrKey string, copyErr error) error {
					log.G(ctx).WithError(copyErr).Debugf("failed to copy xattr %q", xattrKey)
					return nil
				}
				copyDirOpts := []fs.CopyDirOpt{
					fs.WithXAttrErrorHandler(xattrErrorHandler),
				}
				if err = fs.CopyDir(td, parentDir, copyDirOpts...); err != nil {
					return fmt.Errorf("copying of parent failed: %w", err)
				}
			}

			path = o.getSnapshotDir(s.ID)
			if err = os.Rename(td, path); err != nil {
				return fmt.Errorf("failed to rename: %w", err)
			}
			td = ""
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return o.mounts(s), nil
}

func (o *snapshotter) getSnapshotDir(id string) string {
	return filepath.Join(o.root, "snapshots", id)
}

func (o *snapshotter) mounts(s storage.Snapshot) []mount.Mount {
	var source string

	if len(s.ParentIDs) == 0 || s.Kind == snapshots.KindActive {
		source = o.getSnapshotDir(s.ID)
	} else {
		source = o.getSnapshotDir(s.ParentIDs[0])
	}

	// Return a tmpfs mount descriptor. The directory is already a tmpfs
	// mountpoint, so we use a bind mount so containerd can re-mount it
	// at the container rootfs target.
	opts := []string{"rbind"}
	if s.Kind == snapshots.KindView {
		opts = append(opts, "ro")
	} else {
		opts = append(opts, "rw")
	}

	return []mount.Mount{
		{
			Source:  source,
			Type:    "bind",
			Options: opts,
		},
	}
}

func (o *snapshotter) Close() error {
	return o.ms.Close()
}
