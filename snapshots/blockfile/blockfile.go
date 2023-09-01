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

package blockfile

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/snapshots"
	"github.com/containerd/containerd/snapshots/storage"
	"github.com/containerd/continuity/fs"
)

// viewHookHelper is only used in test for recover the filesystem.
type viewHookHelper func(backingFile string, fsType string, defaultOpts []string) error

// SnapshotterConfig holds the configurable properties for the blockfile snapshotter
type SnapshotterConfig struct {
	// recreateScratch is whether scratch should be recreated even
	// if already exists
	recreateScratch bool

	scratchGenerator func(string) error

	// fsType is the filesystem type for the mount (defaults to ext4)
	fsType string

	// mountOptions are the base options added to the mount (defaults to ["loop"])
	mountOptions []string

	// testViewHookHelper is used to fsck or mount with rw to handle
	// the recovery. If we mount ro for view snapshot, we might hit
	// the issue like
	//
	//  (ext4) INFO: recovery required on readonly filesystem
	//  (ext4) write access unavailable, cannot proceed (try mounting with noload)
	//
	// FIXME(fuweid): I don't hit the readonly issue in ssd storage. But it's
	// easy to reproduce it in slow-storage.
	testViewHookHelper viewHookHelper
}

// Opt is an option to configure the overlay snapshotter
type Opt func(string, *SnapshotterConfig)

// WithScratchFile provides a scratch file which will get copied on startup
// if the scratch file needs to be generated.
func WithScratchFile(src string) Opt {
	return func(root string, config *SnapshotterConfig) {
		config.scratchGenerator = func(dst string) error {
			// Copy src to dst
			if err := copyFileWithSync(dst, src); err != nil {
				return fmt.Errorf("failed to copy scratch: %w", err)
			}
			return nil
		}
	}
}

// WithFSType defines the filesystem type to apply to mounts of the blockfile
func WithFSType(fsType string) Opt {
	return func(root string, config *SnapshotterConfig) {
		config.fsType = fsType
	}
}

// WithMountOptions defines the mount options used for the mount
func WithMountOptions(options []string) Opt {
	return func(root string, config *SnapshotterConfig) {
		config.mountOptions = options
	}

}

// WithRecreateScratch is used to determine that scratch should be recreated
// even if already exists.
func WithRecreateScratch(recreate bool) Opt {
	return func(root string, config *SnapshotterConfig) {
		config.recreateScratch = recreate
	}
}

// withViewHookHelper introduces hook for preparing snapshot for View. It
// should be used in test only.
//
//nolint:nolintlint,unused // not used on all platforms
func withViewHookHelper(fn viewHookHelper) Opt {
	return func(_ string, config *SnapshotterConfig) {
		config.testViewHookHelper = fn
	}
}

type snapshotter struct {
	root    string
	scratch string
	fsType  string
	options []string
	ms      *storage.MetaStore

	testViewHookHelper viewHookHelper
}

// NewSnapshotter returns a Snapshotter which copies layers on the underlying
// file system. A metadata file is stored under the root.
func NewSnapshotter(root string, opts ...Opt) (snapshots.Snapshotter, error) {
	var config SnapshotterConfig
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}

	for _, opt := range opts {
		opt(root, &config)
	}

	scratch := filepath.Join(root, "scratch")
	createScratch := config.recreateScratch
	if !createScratch {
		if _, err := os.Stat(scratch); err != nil {
			if !os.IsNotExist(err) {
				return nil, fmt.Errorf("unable to stat scratch file: %w", err)
			}
			createScratch = true
		}
	}
	if createScratch {
		if config.scratchGenerator == nil {
			return nil, fmt.Errorf("no scratch file generator: %w", plugin.ErrSkipPlugin)
		}
		if err := config.scratchGenerator(scratch); err != nil {
			return nil, fmt.Errorf("failed to generate scratch file: %w", err)
		}
	}

	if config.fsType == "" {
		config.fsType = "ext4"
	}

	if config.mountOptions == nil {
		config.mountOptions = []string{"loop"}
	}

	ms, err := storage.NewMetaStore(filepath.Join(root, "metadata.db"))
	if err != nil {
		return nil, err
	}

	if err := os.Mkdir(filepath.Join(root, "snapshots"), 0700); err != nil && !os.IsExist(err) {
		return nil, err
	}

	return &snapshotter{
		root:    root,
		scratch: scratch,
		fsType:  config.fsType,
		options: config.mountOptions,
		ms:      ms,

		testViewHookHelper: config.testViewHookHelper,
	}, nil
}

// Stat returns the info for an active or committed snapshot by name or
// key.
//
// Should be used for parent resolution, existence checks and to discern
// the kind of snapshot.
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
		if err != nil {
			return err
		}

		// Current usage calculation is an approximation based on the size
		// of the block file - the size of its parent. This does not consider
		// that the filesystem may not support shared extents between the block
		// file and its parents, in which case the accurate calculation would just
		// be the size of the block file. Additionally, this does not take into
		// consideration that file may have been removed before being adding,
		// making the number of shared extents between the parent and the block
		// file smaller than the parent, under reporting actual usage.
		//
		// A more ideal calculation would look like:
		//  size(block) - usage(extent_intersection(block,parent))
		// OR
		//  usage(extent_union(block,parent)) - size(parent)

		if info.Kind == snapshots.KindActive {
			// TODO: Use size calculator from fs package
			st, err := os.Stat(o.getBlockFile(id))
			if err != nil {
				return err
			}
			usage.Size = st.Size()
			usage.Inodes = 1
		}

		if info.Parent != "" {
			// GetInfo returns total number of bytes used by a snapshot (including parent).
			// So subtract parent usage in order to get delta consumed by layer itself.
			_, _, parentUsage, err := storage.GetInfo(ctx, info.Parent)
			if err != nil {
				return err
			}

			usage.Size -= parentUsage.Size
		}

		return err
	})
	if err != nil {
		return snapshots.Usage{}, err
	}

	return usage, nil
}

func (o *snapshotter) Prepare(ctx context.Context, key, parent string, opts ...snapshots.Opt) ([]mount.Mount, error) {
	return o.createSnapshot(ctx, snapshots.KindActive, key, parent, opts)
}

func (o *snapshotter) View(ctx context.Context, key, parent string, opts ...snapshots.Opt) ([]mount.Mount, error) {
	return o.createSnapshot(ctx, snapshots.KindView, key, parent, opts)
}

// Mounts returns the mounts for the transaction identified by key. Can be
// called on an read-write or readonly transaction.
//
// This can be used to recover mounts after calling View or Prepare.
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

		st, err := os.Stat(o.getBlockFile(id))
		if err != nil {
			return err
		}

		usage := snapshots.Usage{
			Size:   st.Size(),
			Inodes: 1,
		}

		if _, err = storage.CommitActive(ctx, key, name, usage, opts...); err != nil {
			return fmt.Errorf("failed to commit snapshot: %w", err)
		}
		return nil
	})
}

// Remove abandons the transaction identified by key. All resources
// associated with the key will be removed.
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

		path = o.getBlockFile(id)
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
				// May cause inconsistent data on disk
				log.G(ctx).WithError(err1).WithField("path", renamed).Error("failed to rename after failed commit")
			}
		}
		return err
	}
	if renamed != "" {
		if err := os.Remove(renamed); err != nil {
			// Must be cleaned up, any "rm-*" could be removed if no active transactions
			log.G(ctx).WithError(err).WithField("path", renamed).Warnf("failed to remove root filesystem")
		}
	}

	return nil
}

// Walk the committed snapshots.
func (o *snapshotter) Walk(ctx context.Context, fn snapshots.WalkFunc, fs ...string) error {
	return o.ms.WithTransaction(ctx, false, func(ctx context.Context) error {
		return storage.WalkInfo(ctx, fn, fs...)
	})
}

func (o *snapshotter) createSnapshot(ctx context.Context, kind snapshots.Kind, key, parent string, opts []snapshots.Opt) (_ []mount.Mount, err error) {
	var s storage.Snapshot

	err = o.ms.WithTransaction(ctx, true, func(ctx context.Context) error {
		s, err = storage.CreateSnapshot(ctx, kind, key, parent, opts...)
		if err != nil {
			return fmt.Errorf("failed to create snapshot: %w", err)
		}

		var path string
		if len(s.ParentIDs) == 0 || s.Kind == snapshots.KindActive {
			path = o.getBlockFile(s.ID)

			if len(s.ParentIDs) > 0 {
				if err = copyFileWithSync(path, o.getBlockFile(s.ParentIDs[0])); err != nil {
					return fmt.Errorf("copying of parent failed: %w", err)
				}
			} else {
				if err = copyFileWithSync(path, o.scratch); err != nil {
					return fmt.Errorf("copying of scratch failed: %w", err)
				}
			}
		} else {
			path = o.getBlockFile(s.ParentIDs[0])
		}

		if o.testViewHookHelper != nil {
			if err := o.testViewHookHelper(path, o.fsType, o.options); err != nil {
				return fmt.Errorf("failed to handle the viewHookHelper: %w", err)
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return o.mounts(s), nil
}

func (o *snapshotter) getBlockFile(id string) string {
	return filepath.Join(o.root, "snapshots", id)
}

func (o *snapshotter) mounts(s storage.Snapshot) []mount.Mount {
	var (
		mountOptions = o.options
		source       string
	)

	if s.Kind == snapshots.KindView {
		mountOptions = append(mountOptions, "ro")
	} else {
		mountOptions = append(mountOptions, "rw")
	}

	if len(s.ParentIDs) == 0 || s.Kind == snapshots.KindActive {
		source = o.getBlockFile(s.ID)
	} else {
		source = o.getBlockFile(s.ParentIDs[0])
	}

	return []mount.Mount{
		{
			Source:  source,
			Type:    o.fsType,
			Options: mountOptions,
		},
	}
}

// Close closes the snapshotter
func (o *snapshotter) Close() error {
	return o.ms.Close()
}

func copyFileWithSync(target, source string) error {
	// The Go stdlib does not seem to have an efficient os.File.ReadFrom
	// routine for other platforms like it does on Linux with
	// copy_file_range. For Darwin at least we can use clonefile
	// in its place, otherwise if we have a sparse file we'd have
	// a fun surprise waiting below.
	//
	// TODO: Enlighten other platforms (windows?)
	if runtime.GOOS == "darwin" {
		return fs.CopyFile(target, source)
	}

	src, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("failed to open source %s: %w", source, err)
	}
	defer src.Close()
	tgt, err := os.Create(target)
	if err != nil {
		return fmt.Errorf("failed to open target %s: %w", target, err)
	}
	defer tgt.Close()
	defer tgt.Sync()

	_, err = io.Copy(tgt, src)
	return err
}
