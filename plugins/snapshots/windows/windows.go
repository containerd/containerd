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
	"fmt"
	"io"
	"strings"

	"github.com/Microsoft/go-winio"
	winfs "github.com/Microsoft/go-winio/pkg/fs"
	"github.com/Microsoft/hcsshim"
	"github.com/Microsoft/hcsshim/pkg/ociwclayer"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/core/snapshots/storage"
	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/continuity/fs"
	"github.com/containerd/errdefs"
	"github.com/containerd/log"
	"github.com/containerd/platforms"
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
			return NewWindowsSnapshotter(ic.Properties[plugins.PropertyRootDir])
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

// snapshotter for legacy windows layers
type wcowSnapshotter struct {
	*windowsBaseSnapshotter
}

// NewWindowsSnapshotter returns a new windows snapshotter
func NewWindowsSnapshotter(root string) (snapshots.Snapshotter, error) {
	fsType, err := winfs.GetFileSystemType(root)
	if err != nil {
		return nil, err
	}
	if strings.ToLower(fsType) != "ntfs" {
		return nil, fmt.Errorf("%s is not on an NTFS volume - only NTFS volumes are supported: %w", root, errdefs.ErrInvalidArgument)
	}

	baseSn, err := newBaseSnapshotter(root)
	if err != nil {
		return nil, err
	}

	return &wcowSnapshotter{
		windowsBaseSnapshotter: baseSn,
	}, nil
}

func (s *wcowSnapshotter) Prepare(ctx context.Context, key, parent string, opts ...snapshots.Opt) ([]mount.Mount, error) {
	return s.createSnapshot(ctx, snapshots.KindActive, key, parent, opts)
}

func (s *wcowSnapshotter) View(ctx context.Context, key, parent string, opts ...snapshots.Opt) ([]mount.Mount, error) {
	return s.createSnapshot(ctx, snapshots.KindView, key, parent, opts)
}

func (s *wcowSnapshotter) Commit(ctx context.Context, name, key string, opts ...snapshots.Opt) (retErr error) {
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
func (s *wcowSnapshotter) Remove(ctx context.Context, key string) error {
	renamedID, err := s.preRemove(ctx, key)
	if err != nil {
		// wrap as ErrFailedPrecondition so that cleanup of other snapshots can continue
		return fmt.Errorf("%w: %s", errdefs.ErrFailedPrecondition, err)
	}

	if err = hcsshim.DestroyLayer(s.info, renamedID); err != nil {
		// Must be cleaned up, any "rm-*" could be removed if no active transactions
		log.G(ctx).WithError(err).WithField("renamedID", renamedID).Warnf("Failed to remove root filesystem")
	}

	return nil
}

// convertScratchToReadOnlyLayer reimports the layer over itself, to transfer the files from the sandbox.vhdx to the on-disk storage.
func (s *wcowSnapshotter) convertScratchToReadOnlyLayer(ctx context.Context, snapshot storage.Snapshot, path string) (retErr error) {

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
