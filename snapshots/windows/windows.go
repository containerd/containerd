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
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	winfs "github.com/Microsoft/go-winio/pkg/fs"
	"github.com/Microsoft/go-winio/vhd"
	"github.com/Microsoft/hcsshim"
	"github.com/Microsoft/hcsshim/computestorage"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/platforms"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/snapshots"
	"github.com/containerd/containerd/snapshots/storage"
	"github.com/containerd/continuity/fs"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
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
	// Label to control a UVMs scratch space size.
	uvmRootfsSizeLabel = "containerd.io/snapshot/io.microsoft.vm.storage.rootfs.size-gb"
	// Path to the BCD file in a Utility VM image
	uvmBCDPath = "Files\\EFI\\Microsoft\\Boot\\BCD"
)

type snapshotter struct {
	root        string
	info        hcsshim.DriverInfo
	ms          *storage.MetaStore
	scratchLock sync.Mutex
}

// NewSnapshotter returns a new windows snapshotter
func NewSnapshotter(root string) (snapshots.Snapshotter, error) {
	fsType, err := winfs.GetFileSystemType(root)
	if err != nil {
		return nil, err
	}
	if strings.ToLower(fsType) != "ntfs" {
		return nil, errors.Wrapf(errdefs.ErrInvalidArgument, "%s is not on an NTFS volume - only NTFS volumes are supported", root)
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
		return nil, errors.Wrap(err, "failed to get snapshot mount")
	}
	return s.mounts(snapshot), nil
}

func (s *snapshotter) Commit(ctx context.Context, name, key string, opts ...snapshots.Opt) error {
	ctx, t, err := s.ms.TransactionContext(ctx, true)
	if err != nil {
		return err
	}

	defer func() {
		if err != nil {
			if rerr := t.Rollback(); rerr != nil {
				log.G(ctx).WithError(rerr).Warn("failed to rollback transaction")
			}
		}
	}()

	// grab the existing id
	id, _, _, err := storage.GetInfo(ctx, key)
	if err != nil {
		return err
	}

	usage, err := fs.DiskUsage(ctx, s.getSnapshotDir(id))
	if err != nil {
		return err
	}

	if _, err = storage.CommitActive(ctx, key, name, snapshots.Usage(usage), opts...); err != nil {
		return errors.Wrap(err, "failed to commit snapshot")
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
		return errors.Wrap(err, "failed to remove")
	}

	path := s.getSnapshotDir(id)
	renamedID := "rm-" + id
	renamed := s.getSnapshotDir(renamedID)
	if err := os.Rename(path, renamed); err != nil && !os.IsNotExist(err) {
		if !os.IsPermission(err) {
			return err
		}
		// If permission denied, it's possible that the scratch is still mounted, an
		// artifact after a hard daemon crash for example. Worth a shot to try detaching it
		// before retrying the rename.
		if detachErr := vhd.DetachVhd(filepath.Join(path, "sandbox.vhdx")); detachErr != nil {
			return errors.Wrapf(err, "failed to detach VHD: %s", detachErr)
		}
		if renameErr := os.Rename(path, renamed); renameErr != nil && !os.IsNotExist(renameErr) {
			return errors.Wrapf(err, "second rename attempt following detach failed: %s", renameErr)
		}
	}

	if err := t.Commit(); err != nil {
		if err1 := os.Rename(renamed, path); err1 != nil {
			// May cause inconsistent data on disk
			log.G(ctx).WithError(err1).WithField("path", renamed).Errorf("Failed to rename after failed commit")
		}
		return errors.Wrap(err, "failed to commit")
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
		return nil, errors.Wrap(err, "failed to create snapshot")
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
					return nil, errors.Wrapf(err, "failed to parse label %q=%q", rootfsSizeLabel, sizeGBstr)
				}
				sizeGB = int(i32)
			}

			var UVMsizeGB int
			if sizeGBstr, ok := snapshotInfo.Labels[uvmRootfsSizeLabel]; ok {
				i32, err := strconv.ParseInt(sizeGBstr, 10, 32)
				if err != nil {
					return nil, errors.Wrapf(err, "failed to parse label %q=%q", uvmRootfsSizeLabel, sizeGBstr)
				}
				UVMsizeGB = int(i32)
			}

			var makeUVMScratch bool
			if _, ok := snapshotInfo.Labels[uvmScratchLabel]; ok {
				makeUVMScratch = true
			}

			// This has to be run first to avoid clashing with the containers sandbox.vhdx.
			if makeUVMScratch {
				if err := s.createUVMScratchLayer(ctx, snDir, parentLayerPaths, UVMsizeGB); err != nil {
					return nil, errors.Wrap(err, "failed to make UVM's scratch layer")
				}
			}
			if err := s.createScratchLayer(ctx, snDir, parentLayerPaths, sizeGB); err != nil {
				return nil, errors.Wrap(err, "failed to create scratch layer")
			}
		}
	}

	if err := t.Commit(); err != nil {
		return nil, errors.Wrap(err, "commit failed")
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
	s.scratchLock.Lock()
	defer s.scratchLock.Unlock()

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
			return errors.Wrapf(err, "failed to create scratch vhdx at %q", baseLayer)
		}
	}

	dest := filepath.Join(snDir, "sandbox.vhdx")
	if err := copyFile(templateDiffDisk, dest); err != nil {
		return err
	}

	if expand {
		gbToByte := 1024 * 1024 * 1024
		if err := hcsshim.ExpandSandboxSize(s.info, filepath.Base(snDir), uint64(gbToByte*sizeGB)); err != nil {
			return errors.Wrapf(err, "failed to expand sandbox vhdx size to %d GB", sizeGB)
		}
	}
	return nil
}

// This handles creating the UVMs scratch layer. This is a bit different from the containers scratch layer in
// that expanding the volume doesn't work, so a new set of disks will have to be made. Processing the UVM layer also
// renders any previously made disks unusable as the bcd store gets updated to point to the partition ID of the newly created
// disk. To work around this, we keep a cache of the bcd stores and disks so we can swap out with the correct bcd <--> disk
// mapping.
func (s *snapshotter) createUVMScratchLayer(ctx context.Context, snDir string, parentLayers []string, sizeGB int) error {
	s.scratchLock.Lock()
	defer s.scratchLock.Unlock()

	// Hardcoded size of the UVMs scratch in the original HCS storage calls. If no size is specified
	// just keep using this default.
	if sizeGB == 0 {
		sizeGB = 10
	}

	parentLen := len(parentLayers)
	if parentLen == 0 {
		return errors.New("no parent layers present")
	}
	baseLayer := parentLayers[parentLen-1]

	// Make sure base layer has a UtilityVM folder.
	uvmPath := filepath.Join(baseLayer, "UtilityVM")
	if _, err := os.Stat(uvmPath); os.IsNotExist(err) {
		return errors.Wrapf(err, "failed to find UtilityVM directory in base layer %q", baseLayer)
	}

	var (
		newDisks         = sizeGB > 0 && sizeGB != 10
		templateBase     = filepath.Join(uvmPath, "SystemTemplateBase.vhdx")
		templateDiffDisk = filepath.Join(uvmPath, "SystemTemplate.vhdx")
		bcdName          = fmt.Sprintf("BCD_%d", sizeGB)
		bcdCache         = filepath.Join(uvmPath, "bcdcache")
		bcdDestination   = filepath.Join(bcdCache, bcdName)
		bcdSource        = filepath.Join(uvmPath, uvmBCDPath)
	)

	// If the cache doesn't exist this is the first time this image is being used to
	// create a vm isolated container. Cache the original BCD entry.
	if _, err := os.Stat(bcdCache); os.IsNotExist(err) {
		if err := os.MkdirAll(bcdCache, 0777); err != nil {
			return errors.Wrap(err, "failed to make BCD cache")
		}
		if err := copyFile(bcdSource, filepath.Join(bcdCache, fmt.Sprintf("BCD_%d", 10))); err != nil {
			return errors.Wrap(err, "failed to copy bcd entry")
		}
	}

	if newDisks {
		templateBase = filepath.Join(uvmPath, fmt.Sprintf("SystemTemplateBase_%d.vhdx", sizeGB))
		templateDiffDisk = filepath.Join(uvmPath, fmt.Sprintf("SystemTemplate_%d.vhdx", sizeGB))
	}

	if _, err := os.Stat(templateDiffDisk); os.IsNotExist(err) && newDisks {
		if err := computestorage.SetupUtilityVMBaseLayer(ctx, uvmPath, templateBase, templateDiffDisk, uint64(sizeGB)); err != nil {
			return errors.Wrap(err, "failed to create UVM scratch vhdx")
		}
		if err := copyFile(bcdSource, bcdDestination); err != nil {
			return err
		}
	} else {
		// Disk exists. Make sure bcd entry also does.
		if _, err := os.Stat(bcdDestination); os.IsNotExist(err) {
			return errors.Wrap(err, "failed to find bcd entry for UVM image")
		}
		if err := copyFile(bcdDestination, bcdSource); err != nil {
			return err
		}
	}

	// Move the sandbox.vhdx into a nested vm folder to avoid clashing with a containers sandbox.vhdx.
	vmScratchDir := filepath.Join(snDir, "vm")
	if err := os.MkdirAll(vmScratchDir, 0777); err != nil {
		return errors.Wrap(err, "failed to make `vm` directory for vm's scratch space")
	}

	return copyFile(templateDiffDisk, filepath.Join(vmScratchDir, "sandbox.vhdx"))
}

func copyFile(source, dest string) error {
	scratchSource, err := os.OpenFile(source, os.O_RDWR, 0700)
	if err != nil {
		return errors.Wrapf(err, "failed to open %s", source)
	}
	defer scratchSource.Close()

	f, err := os.OpenFile(dest, os.O_RDWR|os.O_CREATE, 0700)
	if err != nil {
		return errors.Wrap(err, "failed to create sandbox.vhdx in snapshot")
	}
	defer f.Close()

	if _, err := io.Copy(f, scratchSource); err != nil {
		os.Remove(dest)
		return errors.Wrapf(err, "failed to copy cached %q to %q in snapshot", source, dest)
	}

	return nil
}
