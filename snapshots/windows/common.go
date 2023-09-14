//go:build windows
// +build windows

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

package windows

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Microsoft/hcsshim"
	"github.com/containerd/containerd/v2/mount"
	"github.com/containerd/containerd/v2/snapshots"
	"github.com/containerd/containerd/v2/snapshots/storage"
	"github.com/containerd/continuity/fs"
	"github.com/containerd/log"
)

// windowsBaseSnapshotter is a type that implements common functionality required by both windows & cimfs
// snapshotters (sort of a base type that windows & cimfs snapshotter types derive from - however, windowsBaseSnapshotter does NOT impelement the full Snapshotter interface).  Some functions
// (like Stat, Update) that are identical for both snapshotters are directly implemented in this base
// snapshotter and such functions handle database transaction creation etc. However, the functions that are
// not common don't create a transaction to allow the caller the flexibility of deciding whether to commit or
// abort the transaction.
type windowsBaseSnapshotter struct {
	root string
	ms   *storage.MetaStore
	info hcsshim.DriverInfo
}

func newBaseSnapshotter(root string) (*windowsBaseSnapshotter, error) {
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

	return &windowsBaseSnapshotter{
		root: root,
		ms:   ms,
		info: hcsshim.DriverInfo{HomeDir: filepath.Join(root, "snapshots")},
	}, nil
}

func (w *windowsBaseSnapshotter) getSnapshotDir(id string) string {
	return filepath.Join(w.root, "snapshots", id)
}

func (w *windowsBaseSnapshotter) parentIDsToParentPaths(parentIDs []string) []string {
	parentLayerPaths := make([]string, 0, len(parentIDs))
	for _, ID := range parentIDs {
		parentLayerPaths = append(parentLayerPaths, w.getSnapshotDir(ID))
	}
	return parentLayerPaths
}

func (w *windowsBaseSnapshotter) Stat(ctx context.Context, key string) (info snapshots.Info, err error) {
	err = w.ms.WithTransaction(ctx, false, func(ctx context.Context) error {
		_, info, _, err = storage.GetInfo(ctx, key)
		return err
	})
	if err != nil {
		return snapshots.Info{}, err
	}

	return info, nil
}

func (w *windowsBaseSnapshotter) Update(ctx context.Context, info snapshots.Info, fieldpaths ...string) (_ snapshots.Info, err error) {
	err = w.ms.WithTransaction(ctx, true, func(ctx context.Context) error {
		info, err = storage.UpdateInfo(ctx, info, fieldpaths...)
		return err
	})
	if err != nil {
		return snapshots.Info{}, err
	}

	return info, nil
}

func (w *windowsBaseSnapshotter) Usage(ctx context.Context, key string) (usage snapshots.Usage, err error) {
	var (
		id   string
		info snapshots.Info
	)

	err = w.ms.WithTransaction(ctx, false, func(ctx context.Context) error {
		id, info, usage, err = storage.GetInfo(ctx, key)
		return err
	})
	if err != nil {
		return snapshots.Usage{}, err
	}

	if info.Kind == snapshots.KindActive {
		path := w.getSnapshotDir(id)
		du, err := fs.DiskUsage(ctx, path)
		if err != nil {
			return snapshots.Usage{}, err
		}

		usage = snapshots.Usage(du)
	}

	return usage, nil
}

func (w *windowsBaseSnapshotter) mounts(sn storage.Snapshot, key string) []mount.Mount {
	var (
		roFlag string
	)

	if sn.Kind == snapshots.KindView {
		roFlag = "ro"
	} else {
		roFlag = "rw"
	}

	source := w.getSnapshotDir(sn.ID)
	parentLayerPaths := w.parentIDsToParentPaths(sn.ParentIDs)

	mountType := "windows-layer"

	// error is not checked here, as a string array will never fail to Marshal
	parentLayersJSON, _ := json.Marshal(parentLayerPaths)
	parentLayersOption := mount.ParentLayerPathsFlag + string(parentLayersJSON)

	options := []string{
		roFlag,
	}
	if len(sn.ParentIDs) != 0 {
		options = append(options, parentLayersOption)
	}
	mounts := []mount.Mount{
		{
			Source:  source,
			Type:    mountType,
			Options: options,
		},
	}

	return mounts
}

// Mounts returns the mounts for the transaction identified by key. Can be
// called on an read-write or readonly transaction.
//
// This can be used to recover mounts after calling View or Prepare.
func (w *windowsBaseSnapshotter) Mounts(ctx context.Context, key string) (_ []mount.Mount, err error) {
	var snapshot storage.Snapshot
	err = w.ms.WithTransaction(ctx, false, func(ctx context.Context) error {
		snapshot, err = storage.GetSnapshot(ctx, key)
		if err != nil {
			return fmt.Errorf("failed to get snapshot mount: %w", err)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return w.mounts(snapshot, key), nil
}

// Walk the committed snapshots.
func (w *windowsBaseSnapshotter) Walk(ctx context.Context, fn snapshots.WalkFunc, fs ...string) error {
	return w.ms.WithTransaction(ctx, false, func(ctx context.Context) error {
		return storage.WalkInfo(ctx, fn, fs...)
	})
}

// preRemove prepares for removal of a snapshot by first renaming the snapshot directory and if that succeeds
// removing the snapshot info from the database. Then the caller can decide how to remove the actual renamed
// snapshot directory. Returns the new 'ID' (i.e the directory name after rename).
func (w *windowsBaseSnapshotter) preRemove(ctx context.Context, key string) (string, error) {
	var (
		renamed, path, renamedID string
		restore                  bool
	)

	err := w.ms.WithTransaction(ctx, true, func(ctx context.Context) error {
		id, _, err := storage.Remove(ctx, key)
		if err != nil {
			return fmt.Errorf("failed to remove: %w", err)
		}

		path = w.getSnapshotDir(id)
		renamedID = "rm-" + id
		renamed = w.getSnapshotDir(renamedID)
		if err = os.Rename(path, renamed); err != nil && !os.IsNotExist(err) {
			if !os.IsPermission(err) {
				return err
			}
			// If permission denied, it's possible that the scratch is still mounted, an
			// artifact after a hard daemon crash for example. Worth a shot to try deactivating it
			// before retrying the rename.
			var (
				home, layerID = filepath.Split(path)
				di            = hcsshim.DriverInfo{
					HomeDir: home,
				}
			)

			if deactivateErr := hcsshim.DeactivateLayer(di, layerID); deactivateErr != nil {
				return fmt.Errorf("failed to deactivate layer following failed rename: %s: %w", deactivateErr, err)
			}

			if renameErr := os.Rename(path, renamed); renameErr != nil && !os.IsNotExist(renameErr) {
				return fmt.Errorf("second rename attempt following detach failed: %s: %w", renameErr, err)
			}
		}

		restore = true
		return nil
	})
	if err != nil {
		if restore { // failed to commit
			if err1 := os.Rename(renamed, path); err1 != nil {
				// May cause inconsistent data on disk
				log.G(ctx).WithError(err1).WithField("path", renamed).Error("Failed to rename after failed commit")
			}
		}
		return "", err
	}
	return renamedID, nil
}

// Close closes the snapshotter
func (w *windowsBaseSnapshotter) Close() error {
	return w.ms.Close()
}

func (w *windowsBaseSnapshotter) createSnapshot(ctx context.Context, kind snapshots.Kind, key, parent string, opts []snapshots.Opt) (_ []mount.Mount, err error) {
	var newSnapshot storage.Snapshot
	err = w.ms.WithTransaction(ctx, true, func(ctx context.Context) error {
		newSnapshot, err = storage.CreateSnapshot(ctx, kind, key, parent, opts...)
		if err != nil {
			return fmt.Errorf("failed to create snapshot: %w", err)
		}

		log.G(ctx).Debug("createSnapshot")
		// Create the new snapshot dir
		snDir := w.getSnapshotDir(newSnapshot.ID)
		if err = os.MkdirAll(snDir, 0700); err != nil {
			return fmt.Errorf("failed to create snapshot dir %s: %w", snDir, err)
		}

		if strings.Contains(key, snapshots.UnpackKeyPrefix) {
			// IO/disk space optimization: Do nothing
			//
			// We only need one sandbox.vhdx for the container. Skip making one for this
			// snapshot if this isn't the snapshot that just houses the final sandbox.vhd
			// that will be mounted as the containers scratch. Currently the key for a snapshot
			// where a layer will be extracted to will have the string `extract-` in it.
			return nil
		}

		if len(newSnapshot.ParentIDs) == 0 {
			// A parentless snapshot a new base layer. Valid base layers must have a "Files" folder.
			// When committed, there'll be some post-processing to fill in the rest
			// of the metadata.
			filesDir := filepath.Join(snDir, "Files")
			if err := os.MkdirAll(filesDir, 0700); err != nil {
				return fmt.Errorf("creating Files dir: %w", err)
			}
			return nil
		}

		parentLayerPaths := w.parentIDsToParentPaths(newSnapshot.ParentIDs)
		var snapshotInfo snapshots.Info
		for _, o := range opts {
			o(&snapshotInfo)
		}

		var sizeInBytes uint64
		if sizeGBstr, ok := snapshotInfo.Labels[rootfsSizeInGBLabel]; ok {
			log.G(ctx).Warnf("%q label is deprecated, please use %q instead.", rootfsSizeInGBLabel, rootfsSizeInBytesLabel)

			sizeInGB, err := strconv.ParseUint(sizeGBstr, 10, 32)
			if err != nil {
				return fmt.Errorf("failed to parse label %q=%q: %w", rootfsSizeInGBLabel, sizeGBstr, err)
			}
			sizeInBytes = sizeInGB * 1024 * 1024 * 1024
		}

		// Prefer the newer label in bytes over the deprecated Windows specific GB variant.
		if sizeBytesStr, ok := snapshotInfo.Labels[rootfsSizeInBytesLabel]; ok {
			sizeInBytes, err = strconv.ParseUint(sizeBytesStr, 10, 64)
			if err != nil {
				return fmt.Errorf("failed to parse label %q=%q: %w", rootfsSizeInBytesLabel, sizeBytesStr, err)
			}
		}

		var makeUVMScratch bool
		if _, ok := snapshotInfo.Labels[uvmScratchLabel]; ok {
			makeUVMScratch = true
		}

		// This has to be run first to avoid clashing with the containers sandbox.vhdx.
		if makeUVMScratch {
			if err = w.createUVMScratchLayer(ctx, snDir, parentLayerPaths); err != nil {
				return fmt.Errorf("failed to make UVM's scratch layer: %w", err)
			}
		}
		if err = w.createScratchLayer(ctx, snDir, parentLayerPaths, sizeInBytes); err != nil {
			return fmt.Errorf("failed to create scratch layer: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return w.mounts(newSnapshot, key), nil
}

// This is essentially a recreation of what HCS' CreateSandboxLayer does with some extra bells and
// whistles like expanding the volume if a size is specified.
func (w *windowsBaseSnapshotter) createScratchLayer(ctx context.Context, snDir string, parentLayers []string, sizeInBytes uint64) error {
	parentLen := len(parentLayers)
	if parentLen == 0 {
		return errors.New("no parent layers present")
	}

	baseLayer := parentLayers[parentLen-1]
	templateDiffDisk := filepath.Join(baseLayer, "blank.vhdx")
	dest := filepath.Join(snDir, "sandbox.vhdx")
	if err := copyScratchDisk(templateDiffDisk, dest); err != nil {
		return err
	}

	if sizeInBytes != 0 {
		if err := hcsshim.ExpandSandboxSize(w.info, filepath.Base(snDir), sizeInBytes); err != nil {
			return fmt.Errorf("failed to expand sandbox vhdx size to %d bytes: %w", sizeInBytes, err)
		}
	}
	return nil
}

// This handles creating the UVMs scratch layer.
func (w *windowsBaseSnapshotter) createUVMScratchLayer(ctx context.Context, snDir string, parentLayers []string) error {
	parentLen := len(parentLayers)
	if parentLen == 0 {
		return errors.New("no parent layers present")
	}
	baseLayer := parentLayers[parentLen-1]

	// Make sure base layer has a UtilityVM folder.
	uvmPath := filepath.Join(baseLayer, "UtilityVM")
	if _, err := os.Stat(uvmPath); os.IsNotExist(err) {
		return fmt.Errorf("failed to find UtilityVM directory in base layer %q: %w", baseLayer, err)
	}

	templateDiffDisk := filepath.Join(uvmPath, "SystemTemplate.vhdx")

	// Check if SystemTemplate disk doesn't exist for some reason (this should be made during the unpacking
	// of the base layer).
	if _, err := os.Stat(templateDiffDisk); os.IsNotExist(err) {
		return fmt.Errorf("%q does not exist in Utility VM image", templateDiffDisk)
	}

	// Move the sandbox.vhdx into a nested vm folder to avoid clashing with a containers sandbox.vhdx.
	vmScratchDir := filepath.Join(snDir, "vm")
	if err := os.MkdirAll(vmScratchDir, 0777); err != nil {
		return fmt.Errorf("failed to make `vm` directory for vm's scratch space: %w", err)
	}

	return copyScratchDisk(templateDiffDisk, filepath.Join(vmScratchDir, "sandbox.vhdx"))
}

func copyScratchDisk(source, dest string) error {
	scratchSource, err := os.OpenFile(source, os.O_RDWR, 0700)
	if err != nil {
		return fmt.Errorf("failed to open %s: %w", source, err)
	}
	defer scratchSource.Close()

	f, err := os.OpenFile(dest, os.O_RDWR|os.O_CREATE, 0700)
	if err != nil {
		return fmt.Errorf("failed to create sandbox.vhdx in snapshot: %w", err)
	}
	defer f.Close()

	if _, err := io.Copy(f, scratchSource); err != nil {
		os.Remove(dest)
		return fmt.Errorf("failed to copy cached %q to %q in snapshot: %w", source, dest, err)
	}
	return nil
}
