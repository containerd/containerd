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

	"github.com/Microsoft/go-winio"
	winfs "github.com/Microsoft/go-winio/pkg/fs"
	"github.com/Microsoft/hcsshim"
	"github.com/Microsoft/hcsshim/computestorage"
	"github.com/Microsoft/hcsshim/pkg/ociwclayer"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/platforms"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/snapshots"
	"github.com/containerd/containerd/snapshots/storage"
	"github.com/containerd/continuity/fs"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func init() {
	plugin.Register(&plugin.Registration{
		Type: plugin.SnapshotPlugin,
		ID:   "windows",
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			ic.Meta.Platforms = []ocispec.Platform{platforms.DefaultSpec()}
			return NewSnapshotter(ic.Root)
		},
	})
}

const (
	// Label to specify that we should make a scratch space for a UtilityVM.
	uvmScratchLabel = "containerd.io/snapshot/io.microsoft.vm.storage.scratch"
	// Label to control a containers scratch space size (sandbox.vhdx).
	rootfsSizeLabel = "containerd.io/snapshot/io.microsoft.container.storage.rootfs.size-gb"
)

type snapshotter struct {
	root string
	info hcsshim.DriverInfo
	ms   *storage.MetaStore
}

// NewSnapshotter returns a new windows snapshotter
func NewSnapshotter(root string) (snapshots.Snapshotter, error) {
	fsType, err := winfs.GetFileSystemType(root)
	if err != nil {
		return nil, err
	}
	if strings.ToLower(fsType) != "ntfs" {
		return nil, fmt.Errorf("%s is not on an NTFS volume - only NTFS volumes are supported: %w", root, errdefs.ErrInvalidArgument)
	}

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
		info: hcsshim.DriverInfo{
			HomeDir: filepath.Join(root, "snapshots"),
		},
		root: root,
		ms:   ms,
	}, nil
}

// Stat returns the info for an active or committed snapshot by name or
// key.
//
// Should be used for parent resolution, existence checks and to discern
// the kind of snapshot.
func (s *snapshotter) Stat(ctx context.Context, key string) (snapshots.Info, error) {
	ctx, t, err := s.ms.TransactionContext(ctx, false)
	if err != nil {
		return snapshots.Info{}, err
	}
	defer t.Rollback()

	_, info, _, err := storage.GetInfo(ctx, key)
	return info, err
}

func (s *snapshotter) Update(ctx context.Context, info snapshots.Info, fieldpaths ...string) (snapshots.Info, error) {
	ctx, t, err := s.ms.TransactionContext(ctx, true)
	if err != nil {
		return snapshots.Info{}, err
	}
	defer t.Rollback()

	info, err = storage.UpdateInfo(ctx, info, fieldpaths...)
	if err != nil {
		return snapshots.Info{}, err
	}

	if err := t.Commit(); err != nil {
		return snapshots.Info{}, err
	}

	return info, nil
}

func (s *snapshotter) Usage(ctx context.Context, key string) (snapshots.Usage, error) {
	ctx, t, err := s.ms.TransactionContext(ctx, false)
	if err != nil {
		return snapshots.Usage{}, err
	}
	id, info, usage, err := storage.GetInfo(ctx, key)
	t.Rollback() // transaction no longer needed at this point.

	if err != nil {
		return snapshots.Usage{}, err
	}

	if info.Kind == snapshots.KindActive {
		path := s.getSnapshotDir(id)
		du, err := fs.DiskUsage(ctx, path)
		if err != nil {
			return snapshots.Usage{}, err
		}

		usage = snapshots.Usage(du)
	}

	return usage, nil
}

func (s *snapshotter) Prepare(ctx context.Context, key, parent string, opts ...snapshots.Opt) ([]mount.Mount, error) {
	return s.createSnapshot(ctx, snapshots.KindActive, key, parent, opts)
}

func (s *snapshotter) View(ctx context.Context, key, parent string, opts ...snapshots.Opt) ([]mount.Mount, error) {
	return s.createSnapshot(ctx, snapshots.KindView, key, parent, opts)
}

// Mounts returns the mounts for the transaction identified by key. Can be
// called on an read-write or readonly transaction.
//
// This can be used to recover mounts after calling View or Prepare.
func (s *snapshotter) Mounts(ctx context.Context, key string) ([]mount.Mount, error) {
	ctx, t, err := s.ms.TransactionContext(ctx, false)
	if err != nil {
		return nil, err
	}
	defer t.Rollback()

	snapshot, err := storage.GetSnapshot(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("failed to get snapshot mount: %w", err)
	}
	return s.mounts(snapshot), nil
}

func (s *snapshotter) Commit(ctx context.Context, name, key string, opts ...snapshots.Opt) (retErr error) {
	ctx, t, err := s.ms.TransactionContext(ctx, true)
	if err != nil {
		return err
	}

	defer func() {
		if retErr != nil {
			if rerr := t.Rollback(); rerr != nil {
				log.G(ctx).WithError(rerr).Warn("failed to rollback transaction")
			}
		}
	}()

	// grab the existing id
	id, _, _, err := storage.GetInfo(ctx, key)
	if err != nil {
		return fmt.Errorf("failed to get storage info for %s: %w", key, err)
	}

	snapshot, err := storage.GetSnapshot(ctx, key)
	if err != nil {
		return err
	}

	path := s.getSnapshotDir(id)

	// If (windowsDiff).Apply was used to populate this layer, then it's already in the 'committed' state.
	// See createSnapshot below for more details
	if !strings.Contains(key, snapshots.UnpackKeyPrefix) {
		if err := s.convertScratchToReadOnlyLayer(ctx, snapshot, path); err != nil {
			return err
		}
	}

	usage, err := fs.DiskUsage(ctx, path)
	if err != nil {
		return fmt.Errorf("failed to collect disk usage of snapshot storage: %s: %w", path, err)
	}

	if _, err := storage.CommitActive(ctx, key, name, snapshots.Usage(usage), opts...); err != nil {
		return fmt.Errorf("failed to commit snapshot: %w", err)
	}
	return t.Commit()
}

// Remove abandons the transaction identified by key. All resources
// associated with the key will be removed.
func (s *snapshotter) Remove(ctx context.Context, key string) error {
	ctx, t, err := s.ms.TransactionContext(ctx, true)
	if err != nil {
		return err
	}
	defer t.Rollback()

	id, _, err := storage.Remove(ctx, key)
	if err != nil {
		return fmt.Errorf("failed to remove: %w", err)
	}

	path := s.getSnapshotDir(id)
	renamedID := "rm-" + id
	renamed := s.getSnapshotDir(renamedID)
	if err := os.Rename(path, renamed); err != nil && !os.IsNotExist(err) {
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

	if err := t.Commit(); err != nil {
		if err1 := os.Rename(renamed, path); err1 != nil {
			// May cause inconsistent data on disk
			log.G(ctx).WithError(err1).WithField("path", renamed).Error("Failed to rename after failed commit")
		}
		return fmt.Errorf("failed to commit: %w", err)
	}

	if err := hcsshim.DestroyLayer(s.info, renamedID); err != nil {
		// Must be cleaned up, any "rm-*" could be removed if no active transactions
		log.G(ctx).WithError(err).WithField("path", renamed).Warnf("Failed to remove root filesystem")
	}

	return nil
}

// Walk the committed snapshots.
func (s *snapshotter) Walk(ctx context.Context, fn snapshots.WalkFunc, fs ...string) error {
	ctx, t, err := s.ms.TransactionContext(ctx, false)
	if err != nil {
		return err
	}
	defer t.Rollback()

	return storage.WalkInfo(ctx, fn, fs...)
}

// Close closes the snapshotter
func (s *snapshotter) Close() error {
	return s.ms.Close()
}

func (s *snapshotter) mounts(sn storage.Snapshot) []mount.Mount {
	var (
		roFlag           string
		source           string
		parentLayerPaths []string
	)

	if sn.Kind == snapshots.KindView {
		roFlag = "ro"
	} else {
		roFlag = "rw"
	}

	if len(sn.ParentIDs) == 0 || sn.Kind == snapshots.KindActive {
		source = s.getSnapshotDir(sn.ID)
		parentLayerPaths = s.parentIDsToParentPaths(sn.ParentIDs)
	} else {
		source = s.getSnapshotDir(sn.ParentIDs[0])
		parentLayerPaths = s.parentIDsToParentPaths(sn.ParentIDs[1:])
	}

	// error is not checked here, as a string array will never fail to Marshal
	parentLayersJSON, _ := json.Marshal(parentLayerPaths)
	parentLayersOption := mount.ParentLayerPathsFlag + string(parentLayersJSON)

	var mounts []mount.Mount
	mounts = append(mounts, mount.Mount{
		Source: source,
		Type:   "windows-layer",
		Options: []string{
			roFlag,
			parentLayersOption,
		},
	})

	return mounts
}

func (s *snapshotter) getSnapshotDir(id string) string {
	return filepath.Join(s.root, "snapshots", id)
}

func (s *snapshotter) createSnapshot(ctx context.Context, kind snapshots.Kind, key, parent string, opts []snapshots.Opt) ([]mount.Mount, error) {
	ctx, t, err := s.ms.TransactionContext(ctx, true)
	if err != nil {
		return nil, err
	}
	defer t.Rollback()

	newSnapshot, err := storage.CreateSnapshot(ctx, kind, key, parent, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create snapshot: %w", err)
	}

	if kind == snapshots.KindActive {
		log.G(ctx).Debug("createSnapshot active")
		// Create the new snapshot dir
		snDir := s.getSnapshotDir(newSnapshot.ID)
		if err := os.MkdirAll(snDir, 0700); err != nil {
			return nil, err
		}

		// IO/disk space optimization
		//
		// We only need one sandbox.vhdx for the container. Skip making one for this
		// snapshot if this isn't the snapshot that just houses the final sandbox.vhd
		// that will be mounted as the containers scratch. Currently the key for a snapshot
		// where a layer will be extracted to will have the string `extract-` in it.
		if !strings.Contains(key, snapshots.UnpackKeyPrefix) {
			parentLayerPaths := s.parentIDsToParentPaths(newSnapshot.ParentIDs)

			var snapshotInfo snapshots.Info
			for _, o := range opts {
				o(&snapshotInfo)
			}

			var sizeGB int
			if sizeGBstr, ok := snapshotInfo.Labels[rootfsSizeLabel]; ok {
				i32, err := strconv.ParseInt(sizeGBstr, 10, 32)
				if err != nil {
					return nil, fmt.Errorf("failed to parse label %q=%q: %w", rootfsSizeLabel, sizeGBstr, err)
				}
				sizeGB = int(i32)
			}

			var makeUVMScratch bool
			if _, ok := snapshotInfo.Labels[uvmScratchLabel]; ok {
				makeUVMScratch = true
			}

			// This has to be run first to avoid clashing with the containers sandbox.vhdx.
			if makeUVMScratch {
				if err := s.createUVMScratchLayer(ctx, snDir, parentLayerPaths); err != nil {
					return nil, fmt.Errorf("failed to make UVM's scratch layer: %w", err)
				}
			}
			if err := s.createScratchLayer(ctx, snDir, parentLayerPaths, sizeGB); err != nil {
				return nil, fmt.Errorf("failed to create scratch layer: %w", err)
			}
		}
	}

	if err := t.Commit(); err != nil {
		return nil, fmt.Errorf("commit failed: %w", err)
	}

	return s.mounts(newSnapshot), nil
}

func (s *snapshotter) parentIDsToParentPaths(parentIDs []string) []string {
	var parentLayerPaths []string
	for _, ID := range parentIDs {
		parentLayerPaths = append(parentLayerPaths, s.getSnapshotDir(ID))
	}
	return parentLayerPaths
}

// This is essentially a recreation of what HCS' CreateSandboxLayer does with some extra bells and
// whistles like expanding the volume if a size is specified. This will create a 1GB scratch
// vhdx to be used if a different sized scratch that is not equal to the default of 20 is requested.
func (s *snapshotter) createScratchLayer(ctx context.Context, snDir string, parentLayers []string, sizeGB int) error {
	parentLen := len(parentLayers)
	if parentLen == 0 {
		return errors.New("no parent layers present")
	}
	baseLayer := parentLayers[parentLen-1]

	var (
		templateBase     = filepath.Join(baseLayer, "blank-base.vhdx")
		templateDiffDisk = filepath.Join(baseLayer, "blank.vhdx")
		newDisks         = sizeGB > 0 && sizeGB < 20
		expand           = sizeGB > 0 && sizeGB != 20
	)

	// If a size greater than 0 and less than 20 (the default size produced by hcs)
	// was specified we make a new set of disks to be used. We make it a 1GB disk and just
	// expand it to the size specified so for future container runs we don't need to remake a disk.
	if newDisks {
		templateBase = filepath.Join(baseLayer, "scratch.vhdx")
		templateDiffDisk = filepath.Join(baseLayer, "scratch-diff.vhdx")
	}

	if _, err := os.Stat(templateDiffDisk); os.IsNotExist(err) {
		// Scratch disk not present so lets make it.
		if err := computestorage.SetupContainerBaseLayer(ctx, baseLayer, templateBase, templateDiffDisk, 1); err != nil {
			return fmt.Errorf("failed to create scratch vhdx at %q: %w", baseLayer, err)
		}
	}

	dest := filepath.Join(snDir, "sandbox.vhdx")
	if err := copyScratchDisk(templateDiffDisk, dest); err != nil {
		return err
	}

	if expand {
		gbToByte := 1024 * 1024 * 1024
		if err := hcsshim.ExpandSandboxSize(s.info, filepath.Base(snDir), uint64(gbToByte*sizeGB)); err != nil {
			return fmt.Errorf("failed to expand sandbox vhdx size to %d GB: %w", sizeGB, err)
		}
	}
	return nil
}

// convertScratchToReadOnlyLayer reimports the layer over itself, to transfer the files from the sandbox.vhdx to the on-disk storage.
func (s *snapshotter) convertScratchToReadOnlyLayer(ctx context.Context, snapshot storage.Snapshot, path string) (retErr error) {

	// TODO darrenstahlmsft: When this is done isolated, we should disable these.
	// it currently cannot be disabled, unless we add ref counting. Since this is
	// temporary, leaving it enabled is OK for now.
	// https://github.com/containerd/containerd/issues/1681
	if err := winio.EnableProcessPrivileges([]string{winio.SeBackupPrivilege, winio.SeRestorePrivilege}); err != nil {
		return fmt.Errorf("failed to enable necessary privileges: %w", err)
	}

	parentLayerPaths := s.parentIDsToParentPaths(snapshot.ParentIDs)
	reader, writer := io.Pipe()

	go func() {
		err := ociwclayer.ExportLayerToTar(ctx, writer, path, parentLayerPaths)
		writer.CloseWithError(err)
	}()

	if _, err := ociwclayer.ImportLayerFromTar(ctx, reader, path, parentLayerPaths); err != nil {
		return fmt.Errorf("failed to reimport snapshot: %w", err)
	}

	if _, err := io.Copy(io.Discard, reader); err != nil {
		return fmt.Errorf("failed discarding extra data in import stream: %w", err)
	}

	// NOTE: We do not delete the sandbox.vhdx here, as that will break later calls to
	// ociwclayer.ExportLayerToTar for this snapshot.
	// As a consequence, the data for this layer is held twice, once on-disk and once
	// in the sandbox.vhdx.
	// TODO: This is either a bug or misfeature in hcsshim, so will need to be resolved
	// there first.

	return nil
}

// This handles creating the UVMs scratch layer.
func (s *snapshotter) createUVMScratchLayer(ctx context.Context, snDir string, parentLayers []string) error {
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
