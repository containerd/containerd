//go:build linux

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

package erofs

import (
	"context"
	"fmt"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/snapshots"
	"github.com/containerd/containerd/snapshots/erofs/loop"
	"github.com/containerd/containerd/snapshots/storage"
	"github.com/containerd/continuity/fs"
	"github.com/containerd/log"
	"github.com/pkg/errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	criContainerAnno = "containerd.io/snapshot/cri.container"
)

// SnapshotterConfig is used to configure the overlay snapshotter instance
type SnapshotterConfig struct {
	asyncRemove bool
}

// Opt is an option to configure the overlay snapshotter
type Opt func(config *SnapshotterConfig) error

// AsynchronousRemove defers removal of filesystem content until
// the Cleanup method is called. Removals will make the snapshot
// referred to by the key unavailable and make the key immediately
// available for re-use.
func AsynchronousRemove(config *SnapshotterConfig) error {
	config.asyncRemove = true
	return nil
}

type MetaStore interface {
	TransactionContext(ctx context.Context, writable bool) (context.Context, storage.Transactor, error)
	WithTransaction(ctx context.Context, writable bool, fn storage.TransactionCallback) error
	Close() error
}

type snapshotter struct {
	root        string
	ms          *storage.MetaStore
	asyncRemove bool
	loopDriver  loop.Driver
}

// NewSnapshotter returns a Snapshotter which uses overlayfs. The overlayfs
// diffs are stored under the provided root. A metadata file is stored under
// the root.
func NewSnapshotter(root string, opts ...Opt) (snapshots.Snapshotter, error) {
	var config SnapshotterConfig
	for _, opt := range opts {
		if err := opt(&config); err != nil {
			return nil, err
		}
	}

	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}

	//TODO: check if erofs is supported

	ms, err := storage.NewMetaStore(filepath.Join(root, "metadata.db"))
	if err != nil {
		return nil, err
	}
	if err := os.Mkdir(filepath.Join(root, "snapshots"), 0700); err != nil && !os.IsExist(err) {
		return nil, err
	}

	loopDriver, _ := loop.NewLoopDriver()
	//TODO: erofs needed global mount options

	return &snapshotter{
		root:        root,
		ms:          ms,
		asyncRemove: config.asyncRemove,
		loopDriver:  loopDriver,
	}, nil
}

// Stat returns the info for an active or committed snapshot by name or
// key.
//
// Should be used for parent resolution, existence checks and to discern
// the kind of snapshot.
func (o *snapshotter) Stat(ctx context.Context, key string) (info snapshots.Info, err error) {
	if err := o.ms.WithTransaction(ctx, false, func(ctx context.Context) error {
		_, info, _, err = storage.GetInfo(ctx, key)
		return err
	}); err != nil {
		return info, err
	}

	if info.Labels == nil {
		info.Labels = make(map[string]string)
	}

	return info, nil
}

func (o *snapshotter) Update(ctx context.Context, info snapshots.Info, fieldpaths ...string) (newInfo snapshots.Info, err error) {
	err = o.ms.WithTransaction(ctx, true, func(ctx context.Context) error {
		newInfo, err = storage.UpdateInfo(ctx, info, fieldpaths...)
		if err != nil {
			return err
		}
		return nil
	})
	return newInfo, err
}

// Usage returns the resources taken by the snapshot identified by key.
//
// For active snapshots, this will scan the usage of the overlay "diff" (aka
// "upper") directory and may take some time.
//
// For committed snapshots, the value is returned from the metadata database.
func (o *snapshotter) Usage(ctx context.Context, key string) (_ snapshots.Usage, err error) {
	var (
		usage snapshots.Usage
		info  snapshots.Info
		id    string
	)
	if err := o.ms.WithTransaction(ctx, false, func(ctx context.Context) error {
		id, info, usage, err = storage.GetInfo(ctx, key)
		return err
	}); err != nil {
		return usage, err
	}

	if info.Kind == snapshots.KindActive {
		upperPath := o.plainPath(id)
		du, err := fs.DiskUsage(ctx, upperPath)
		if err != nil {
			// TODO(stevvooe): Consider not reporting an error in this case.
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

// Mounts returns the mounts for the transaction identified by key. Can be
// called on an read-write or readonly transaction.
//
// This can be used to recover mounts after calling View or Prepare.
func (o *snapshotter) Mounts(ctx context.Context, key string) (_ []mount.Mount, err error) {
	var s storage.Snapshot
	var info snapshots.Info
	if err := o.ms.WithTransaction(ctx, false, func(ctx context.Context) error {
		s, err = storage.GetSnapshot(ctx, key)
		if err != nil {
			return fmt.Errorf("failed to get active mount: %w", err)
		}

		_, info, _, err = storage.GetInfo(ctx, key)
		if err != nil {
			return fmt.Errorf("failed to get snapshot info: %w", err)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return o.mounts(ctx, s, info), nil
}

func (o *snapshotter) Commit(ctx context.Context, name, key string, opts ...snapshots.Opt) error {
	return o.ms.WithTransaction(ctx, true, func(ctx context.Context) error {
		// grab the existing id
		id, _, _, err := storage.GetInfo(ctx, key)
		if err != nil {
			return err
		}

		usage, err := fs.DiskUsage(ctx, o.plainPath(id))
		if err != nil {
			return err
		}

		if _, err = storage.CommitActive(ctx, key, name, snapshots.Usage(usage), opts...); err != nil {
			return fmt.Errorf("failed to commit snapshot %s: %w", key, err)
		}
		src := filepath.Join(o.plainPath(id), "layer.erofs")
		if _, err := os.Stat(src); err == nil {
			o.loopDriver.Prepare(ctx, src)
			log.G(ctx).Infof("prepared loop device for snapshot %s", name)
		}
		return nil
	})
}

// Remove abandons the snapshot identified by key. The snapshot will
// immediately become unavailable and unrecoverable. Disk space will
// be freed up on the next call to `Cleanup`.
func (o *snapshotter) Remove(ctx context.Context, key string) (err error) {
	var removals []string
	// Remove directories after the transaction is closed, failures must not
	// return error since the transaction is committed with the removal
	// key no longer available.
	defer func() {
		if err == nil {
			for _, dir := range removals {
				if err := os.RemoveAll(dir); err != nil {
					log.G(ctx).WithError(err).WithField("path", dir).Warn("failed to remove directory")
				}
			}
		}
	}()
	return o.ms.WithTransaction(ctx, true, func(ctx context.Context) error {

		id, _, err := storage.Remove(ctx, key)
		if err != nil {
			return fmt.Errorf("failed to remove snapshot %s: %w", key, err)
		}
		lowerDir := filepath.Join(o.plainPath(id), "lower")
		o.umount(ctx, lowerDir)

		o.loopDriver.Remove(ctx, filepath.Join(o.plainPath(id), "loop"))

		if !o.asyncRemove {
			removals, err = o.getCleanupDirectories(ctx)
			if err != nil {
				return fmt.Errorf("unable to get directories for removal: %w", err)
			}
		}
		return nil
	})
}

// Walk the snapshots.
func (o *snapshotter) Walk(ctx context.Context, fn snapshots.WalkFunc, fs ...string) error {
	return o.ms.WithTransaction(ctx, false, func(ctx context.Context) error {
		return storage.WalkInfo(ctx, fn, fs...)
	})
}

// Cleanup cleans up disk resources from removed or abandoned snapshots
func (o *snapshotter) Cleanup(ctx context.Context) error {
	cleanup, err := o.cleanupDirectories(ctx)
	if err != nil {
		return err
	}

	for _, dir := range cleanup {
		if err := os.RemoveAll(dir); err != nil {
			log.G(ctx).WithError(err).WithField("path", dir).Warn("failed to remove directory")
		}
	}

	return nil
}

func (o *snapshotter) cleanupDirectories(ctx context.Context) (_ []string, err error) {
	var cleanupDirs []string
	// Get a write transaction to ensure no other write transaction can be entered
	// while the cleanup is scanning.
	if err := o.ms.WithTransaction(ctx, true, func(ctx context.Context) error {
		cleanupDirs, err = o.getCleanupDirectories(ctx)
		return err
	}); err != nil {
		return nil, err
	}
	return cleanupDirs, nil
}

func (o *snapshotter) getCleanupDirectories(ctx context.Context) ([]string, error) {
	ids, err := storage.IDMap(ctx)
	if err != nil {
		return nil, err
	}

	snapshotDir := filepath.Join(o.root, "snapshots")
	fd, err := os.Open(snapshotDir)
	if err != nil {
		return nil, err
	}
	defer fd.Close()

	dirs, err := fd.Readdirnames(0)
	if err != nil {
		return nil, err
	}

	cleanup := []string{}
	for _, d := range dirs {
		if _, ok := ids[d]; ok {
			continue
		}
		cleanup = append(cleanup, filepath.Join(snapshotDir, d))
	}

	return cleanup, nil
}

func validateIDMapping(mapping string) error {
	var (
		hostID int
		ctrID  int
		length int
	)

	if _, err := fmt.Sscanf(mapping, "%d:%d:%d", &ctrID, &hostID, &length); err != nil {
		return err
	}
	// Almost impossible, but snapshots.WithLabels doesn't check it
	if ctrID < 0 || hostID < 0 || length < 0 {
		return fmt.Errorf("invalid mapping \"%d:%d:%d\"", ctrID, hostID, length)
	}
	if ctrID != 0 {
		return fmt.Errorf("container mapping of 0 is only supported")
	}
	return nil
}

func hostID(mapping string) (int, error) {
	var (
		hostID int
		ctrID  int
		length int
	)
	if err := validateIDMapping(mapping); err != nil {
		return -1, fmt.Errorf("invalid mapping: %w", err)
	}
	if _, err := fmt.Sscanf(mapping, "%d:%d:%d", &ctrID, &hostID, &length); err != nil {
		return -1, err
	}
	return hostID, nil
}

func (o *snapshotter) createSnapshot(ctx context.Context, kind snapshots.Kind, key, parent string, opts []snapshots.Opt) (_ []mount.Mount, err error) {
	var (
		s              storage.Snapshot
		td, path       string
		info           snapshots.Info
		containerUpper bool
	)

	var base = snapshots.Info{}
	for _, opt := range opts {
		if err := opt(&base); err != nil {
			return nil, err
		}
	}
	if _, ok := base.Labels[criContainerAnno]; ok {
		containerUpper = true
	}

	defer func() {
		if err != nil {
			if td != "" {
				if err1 := os.RemoveAll(td); err1 != nil {
					log.G(ctx).WithError(err1).Warn("failed to cleanup temp snapshot directory")
				}
			}
			if path != "" {
				if err1 := os.RemoveAll(path); err1 != nil {
					log.G(ctx).WithError(err1).WithField("path", path).Error("failed to reclaim snapshot directory, directory may need removal")
					err = fmt.Errorf("failed to remove path: %v: %w", err1, err)
				}
			}
		}
	}()

	if err := o.ms.WithTransaction(ctx, true, func(ctx context.Context) (err error) {
		snapshotDir := filepath.Join(o.root, "snapshots")
		td, err = o.prepareDirectory(ctx, snapshotDir, kind, containerUpper)
		if err != nil {
			return fmt.Errorf("failed to create prepare snapshot dir: %w", err)
		}

		s, err = storage.CreateSnapshot(ctx, kind, key, parent, opts...)
		if err != nil {
			return fmt.Errorf("failed to create snapshot: %w", err)
		}

		_, info, _, err = storage.GetInfo(ctx, key)
		if err != nil {
			return fmt.Errorf("failed to get snapshot info: %w", err)
		}

		path = filepath.Join(snapshotDir, s.ID)
		if err = os.Rename(td, path); err != nil {
			return fmt.Errorf("failed to rename: %w", err)
		}
		td = ""

		if containerUpper {
			if err := o.mergeDir(ctx, s, path); err != nil {
				return fmt.Errorf("failed to do erofs merge: %w", err)
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return o.mounts(ctx, s, info), nil
}

func (o *snapshotter) prepareDirectory(ctx context.Context, snapshotDir string, kind snapshots.Kind, containerUpper bool) (string, error) {
	td, err := os.MkdirTemp(snapshotDir, "new-")
	if err != nil {
		return "", fmt.Errorf("failed to create temp dir: %w", err)
	}

	if containerUpper {
		if err := os.Mkdir(filepath.Join(td, "fs"), 0755); err != nil {
			return td, err
		}

		if kind == snapshots.KindActive {
			if err := os.Mkdir(filepath.Join(td, "work"), 0711); err != nil {
				return td, err
			}
		}
	}

	return td, nil
}

func (o *snapshotter) mounts(ctx context.Context, s storage.Snapshot, info snapshots.Info) []mount.Mount {
	var options []string
	merged := filepath.Join(o.plainPath(s.ID), "merged.erofs")

	if _, err := os.Stat(merged); err != nil {
		return []mount.Mount{
			{
				Source: o.plainPath(s.ID),
				Type:   "bind",
				Options: append(options,
					"rbind",
					"rw",
				),
			},
		}
	}

	lowerDir := filepath.Join(o.plainPath(s.ID), "lower")
	if _, err := os.Stat(lowerDir); os.IsNotExist(err) {
		mergedLoop, err := o.loopDriver.Get(context.Background(), filepath.Join(o.plainPath(s.ID), "loop"))
		if os.IsNotExist(err) {
			mergedLoop, err = o.loopDriver.Prepare(context.Background(), filepath.Join(o.plainPath(s.ID), "merged.erofs"))
		} else if err != nil {
			log.G(ctx).Infof("failed to prepare loop device for merged %s: %v", filepath.Join(o.plainPath(s.ID), "merged.erofs"), err)
		}

		for i := range s.ParentIDs {
			loopDevice, err := o.loopDriver.Get(context.Background(), filepath.Join(o.plainPath(s.ParentIDs[i]), "loop"))
			if os.IsNotExist(err) {
				log.G(ctx).Infof("loop device for snapshot %s not found, preparing", s.ParentIDs[i])
				loopDevice, err = o.loopDriver.Prepare(context.Background(), filepath.Join(o.plainPath(s.ParentIDs[i]), "layer.erofs"))
			} else if err != nil {
				log.G(ctx).Infof("loop device get failed: %v", err)
			}
			options = append(options, fmt.Sprintf("device=%s", loopDevice))
		}

		lowerMount := mount.Mount{
			Type:    "erofs",
			Source:  mergedLoop,
			Options: options,
		}
		if err := os.MkdirAll(lowerDir, 0777); err != nil {
			log.G(ctx).Errorf("failed to prepare merged dir %v for upper: %v", lowerDir, err)
			return []mount.Mount{
				{
					Source: o.plainPath(s.ID),
					Type:   "bind",
					Options: append(options,
						"rbind",
						"rw",
					),
				},
			}
		}
		if err := lowerMount.Mount(lowerDir); err != nil {
			log.G(ctx).Errorf("failed to mount merged dir %v for upper: %v", lowerDir, err)
			return []mount.Mount{
				{
					Source: o.plainPath(s.ID),
					Type:   "bind",
					Options: append(options,
						"rbind",
						"rw",
					),
				},
			}
		}
	} else if err != nil {
		log.G(ctx).Errorf("failed to stat merged dir for upper: %v", err)
		return []mount.Mount{
			{
				Source: o.plainPath(s.ID),
				Type:   "bind",
				Options: append(options,
					"rbind",
					"rw",
				),
			},
		}
	}

	options = append(options,
		fmt.Sprintf("workdir=%s", o.workPath(s.ID)),
		fmt.Sprintf("upperdir=%s", o.upperPath(s.ID)),
	)
	options = append(options, fmt.Sprintf("lowerdir=%s", lowerDir))
	return []mount.Mount{
		{
			Type:    "overlay",
			Source:  "overlay",
			Options: options,
		},
	}
}

func (o *snapshotter) mergeDir(ctx context.Context, s storage.Snapshot, path string) error {
	mergeTarget := filepath.Join(path, "merged.erofs")
	args := []string{
		mergeTarget,
	}
	for i := range s.ParentIDs {
		args = append(args, filepath.Join(o.plainPath(s.ParentIDs[i]), "layer.erofs"))
	}
	log.G(ctx).Infof("merging %v layers with command: %v", mergeTarget, args)
	output, err := exec.Command("mkfs.erofs", args...).CombinedOutput()
	if err != nil {
		return errors.Wrapf(err, "failed to merge erofs:\n%s", string(output))
	}
	return nil
}

func (o *snapshotter) umount(ctx context.Context, target string) error {
	args := []string{
		"-T",
		target,
		"-P",
		"-o",
		"SOURCE",
	}

	output, err := exec.Command("findmnt", args...).CombinedOutput()
	if err != nil {
		log.G(ctx).Warnf("umount: failed to findmnt target %s: %v", target, err)
	}
	if err := mount.Unmount(target, 0); err != nil {
		return err
	}
	loops := strings.Split(string(output), "\"")
	if len(loops) < 2 {
		log.G(ctx).Infof("umount: findmnt result is: %v", output)
		return nil
	}
	loop := loops[1]
	log.G(ctx).Debugf("umount: trying to ensure %s is cleaned on %s", loop, target)
	for i := 0; i < 30; i++ {
		file, err := os.Open(fmt.Sprintf("/sys/block/%s/loop/backing_file", filepath.Base(loop)))
		if err != nil {
			log.G(ctx).Debugf("umount: open file %s error is: %v", target, err)
			return nil
		}
		buf := make([]byte, 300)
		file.Read(buf)
		file.Close()
		if filepath.Dir(string(buf)) != target {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil
}

func (o *snapshotter) plainPath(id string) string {
	return filepath.Join(o.root, "snapshots", id)
}

func (o *snapshotter) upperPath(id string) string {
	return filepath.Join(o.root, "snapshots", id, "fs")
}

func (o *snapshotter) workPath(id string) string {
	return filepath.Join(o.root, "snapshots", id, "work")
}

// Close closes the snapshotter
func (o *snapshotter) Close() error {
	return o.ms.Close()
}
