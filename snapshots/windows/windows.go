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
	"github.com/Microsoft/hcsshim/pkg/ociwclayer"
	"github.com/containerd/containerd/v2/errdefs"
	"github.com/containerd/containerd/v2/mount"
	"github.com/containerd/containerd/v2/platforms"
	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/containerd/v2/snapshots"
	"github.com/containerd/containerd/v2/snapshots/storage"
	"github.com/containerd/continuity/fs"
	"github.com/containerd/log"
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func init() {
	registry.Register(&plugin.Registration{
		Type: plugins.SnapshotPlugin,
		ID:   "windows",
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			ic.Meta.Platforms = []ocispec.Platform{platforms.DefaultSpec()}
			return NewSnapshotter(ic.Properties[plugins.PropertyRootDir])
		},
	})
}

const (
	// Label to specify that we should make a scratch space for a UtilityVM.
	uvmScratchLabel = "containerd.io/snapshot/io.microsoft.vm.storage.scratch"
	// Label to control a containers scratch space size (sandbox.vhdx).
	//
	// Deprecated: use rootfsSizeInBytesLabel
	rootfsSizeInGBLabel = "containerd.io/snapshot/io.microsoft.container.storage.rootfs.size-gb"
	// rootfsSizeInBytesLabel is a label to control a Windows containers scratch space
	// size in bytes.
	rootfsSizeInBytesLabel = "containerd.io/snapshot/windows/rootfs.sizebytes"
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
func (s *snapshotter) Stat(ctx context.Context, key string) (info snapshots.Info, err error) {
	err = s.ms.WithTransaction(ctx, false, func(ctx context.Context) error {
		_, info, _, err = storage.GetInfo(ctx, key)
		return err
	})
	if err != nil {
		return snapshots.Info{}, err
	}

	return info, nil
}

func (s *snapshotter) Update(ctx context.Context, info snapshots.Info, fieldpaths ...string) (_ snapshots.Info, err error) {
	err = s.ms.WithTransaction(ctx, true, func(ctx context.Context) error {
		info, err = storage.UpdateInfo(ctx, info, fieldpaths...)
		return err
	})
	if err != nil {
		return snapshots.Info{}, err
	}

	return info, nil
}

func (s *snapshotter) Usage(ctx context.Context, key string) (usage snapshots.Usage, err error) {
	var (
		id   string
		info snapshots.Info
	)

	err = s.ms.WithTransaction(ctx, false, func(ctx context.Context) error {
		id, info, usage, err = storage.GetInfo(ctx, key)
		return err
	})
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
func (s *snapshotter) Mounts(ctx context.Context, key string) (_ []mount.Mount, err error) {
	var snapshot storage.Snapshot
	err = s.ms.WithTransaction(ctx, false, func(ctx context.Context) error {
		snapshot, err = storage.GetSnapshot(ctx, key)
		if err != nil {
			return fmt.Errorf("failed to get snapshot mount: %w", err)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return s.mounts(snapshot, key), nil
}

func (s *snapshotter) Commit(ctx context.Context, name, key string, opts ...snapshots.Opt) (retErr error) {
	return s.ms.WithTransaction(ctx, true, func(ctx context.Context) error {
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
			if len(snapshot.ParentIDs) == 0 {
				if err = hcsshim.ConvertToBaseLayer(path); err != nil {
					return err
				}
			} else if err := s.convertScratchToReadOnlyLayer(ctx, snapshot, path); err != nil {
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

		return nil
	})
}

// Remove abandons the transaction identified by key. All resources
// associated with the key will be removed.
func (s *snapshotter) Remove(ctx context.Context, key string) error {
	var (
		renamed, path, renamedID string
		restore                  bool
	)

	err := s.ms.WithTransaction(ctx, true, func(ctx context.Context) error {
		id, _, err := storage.Remove(ctx, key)
		if err != nil {
			return fmt.Errorf("failed to remove: %w", err)
		}

		path = s.getSnapshotDir(id)
		renamedID = "rm-" + id
		renamed = s.getSnapshotDir(renamedID)
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
		return err
	}

	if err = hcsshim.DestroyLayer(s.info, renamedID); err != nil {
		// Must be cleaned up, any "rm-*" could be removed if no active transactions
		log.G(ctx).WithError(err).WithField("path", renamed).Warnf("Failed to remove root filesystem")
	}

	return nil
}

// Walk the committed snapshots.
func (s *snapshotter) Walk(ctx context.Context, fn snapshots.WalkFunc, fs ...string) error {
	return s.ms.WithTransaction(ctx, false, func(ctx context.Context) error {
		return storage.WalkInfo(ctx, fn, fs...)
	})
}

// Close closes the snapshotter
func (s *snapshotter) Close() error {
	return s.ms.Close()
}

func (s *snapshotter) mounts(sn storage.Snapshot, key string) []mount.Mount {
	var (
		roFlag string
	)

	if sn.Kind == snapshots.KindView {
		roFlag = "ro"
	} else {
		roFlag = "rw"
	}

	source := s.getSnapshotDir(sn.ID)
	parentLayerPaths := s.parentIDsToParentPaths(sn.ParentIDs)

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

func (s *snapshotter) getSnapshotDir(id string) string {
	return filepath.Join(s.root, "snapshots", id)
}

func (s *snapshotter) createSnapshot(ctx context.Context, kind snapshots.Kind, key, parent string, opts []snapshots.Opt) (_ []mount.Mount, err error) {
	var newSnapshot storage.Snapshot
	err = s.ms.WithTransaction(ctx, true, func(ctx context.Context) error {
		newSnapshot, err = storage.CreateSnapshot(ctx, kind, key, parent, opts...)
		if err != nil {
			return fmt.Errorf("failed to create snapshot: %w", err)
		}

		log.G(ctx).Debug("createSnapshot")
		// Create the new snapshot dir
		snDir := s.getSnapshotDir(newSnapshot.ID)
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

		parentLayerPaths := s.parentIDsToParentPaths(newSnapshot.ParentIDs)
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
			if err = s.createUVMScratchLayer(ctx, snDir, parentLayerPaths); err != nil {
				return fmt.Errorf("failed to make UVM's scratch layer: %w", err)
			}
		}
		if err = s.createScratchLayer(ctx, snDir, parentLayerPaths, sizeInBytes); err != nil {
			return fmt.Errorf("failed to create scratch layer: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return s.mounts(newSnapshot, key), nil
}

func (s *snapshotter) parentIDsToParentPaths(parentIDs []string) []string {
	var parentLayerPaths []string
	for _, ID := range parentIDs {
		parentLayerPaths = append(parentLayerPaths, s.getSnapshotDir(ID))
	}
	return parentLayerPaths
}

// This is essentially a recreation of what HCS' CreateSandboxLayer does with some extra bells and
// whistles like expanding the volume if a size is specified.
func (s *snapshotter) createScratchLayer(ctx context.Context, snDir string, parentLayers []string, sizeInBytes uint64) error {
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
		if err := hcsshim.ExpandSandboxSize(s.info, filepath.Base(snDir), sizeInBytes); err != nil {
			return fmt.Errorf("failed to expand sandbox vhdx size to %d bytes: %w", sizeInBytes, err)
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

	// It seems that in certain situations, like having the containerd root and state on a file system hosted on a
	// mounted VHDX, we need SeSecurityPrivilege when opening a file with winio.ACCESS_SYSTEM_SECURITY. This happens
	// in the base layer writer in hcsshim when adding a new file.
	if err := winio.RunWithPrivileges([]string{winio.SeSecurityPrivilege}, func() error {
		_, err := ociwclayer.ImportLayerFromTar(ctx, reader, path, parentLayerPaths)
		return err
	}); err != nil {
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
