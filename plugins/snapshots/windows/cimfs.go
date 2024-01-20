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
	"os"
	"path/filepath"
	"strings"

	"github.com/Microsoft/hcsshim"
	"github.com/Microsoft/hcsshim/pkg/cimfs"
	cimlayer "github.com/Microsoft/hcsshim/pkg/ociwclayer/cim"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/core/snapshots/storage"
	"github.com/containerd/containerd/v2/pkg/errdefs"
	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/log"
	"github.com/containerd/platforms"
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// Composite image FileSystem (CimFS) is a new read-only filesystem (similar to overlayFS on Linux) created
// specifically for storing container image layers on windows.  cimFSSnapshotter is a snapshotter that uses
// CimFS to create read-only parent layer snapshots. Each snapshot is represented by a `<snapshot-id>.cim`
// file and some other files (region & objectid files) which hold contents of that snapshot.  Once a cim file for a layer is created it
// can only be used as a read-only layer by mounting it to a volume. Hence, CimFs will not be used when we are
// creating writable layers for container scratch and such. (However, in the future scratch layer of a container can be
// exported to a cim layer and then be used as a parent layer for another container).
type cimFSSnapshotter struct {
	*windowsBaseSnapshotter
	// cimDir is the path to the directory which holds all of the layer cim files.  CimFS needs all the
	// layer cim files to be present in the same directory. Hence, cim files of all the snapshots (even if
	// they are of different images) will be kept in the same directory.
	cimDir string
}

func init() {
	registry.Register(&plugin.Registration{
		Type: plugins.SnapshotPlugin,
		ID:   "cimfs",
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			ic.Meta.Platforms = []ocispec.Platform{platforms.DefaultSpec()}
			return NewCimFSSnapshotter(ic.Properties[plugins.PropertyRootDir])
		},
	})
}

// NewCimFSSnapshotter returns a new CimFS based windows snapshotter
func NewCimFSSnapshotter(root string) (snapshots.Snapshotter, error) {
	if !cimfs.IsCimFSSupported() {
		return nil, fmt.Errorf("host windows version doesn't support CimFS: %w", plugin.ErrSkipPlugin)
	}

	baseSn, err := newBaseSnapshotter(root)
	if err != nil {
		return nil, err
	}

	return &cimFSSnapshotter{
		windowsBaseSnapshotter: baseSn,
		cimDir:                 filepath.Join(baseSn.info.HomeDir, "cim-layers"),
	}, nil
}

// getCimLayerPath returns the path of the cim file for the given snapshot. Note that this function doesn't
// actually check if the cim layer exists it simply does string manipulation to generate the path isCimLayer
// can be used to verify if it is actually a cim layer.
func getCimLayerPath(cimDir, snID string) string {
	return filepath.Join(cimDir, (snID + ".cim"))
}

// isCimLayer checks if the snapshot referred by the given key is actually a cim layer.  With CimFS
// snapshotter all the read-only (i.e image) layers are stored in the cim format while we still use VHDs for
// scratch layers.
func (s *cimFSSnapshotter) isCimLayer(ctx context.Context, key string) (bool, error) {
	id, _, _, err := storage.GetInfo(ctx, key)
	if err != nil {
		return false, fmt.Errorf("get snapshot info: %w", err)
	}
	snCimPath := getCimLayerPath(s.cimDir, id)
	if _, err := os.Stat(snCimPath); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (s *cimFSSnapshotter) Usage(ctx context.Context, key string) (snapshots.Usage, error) {
	baseUsage, err := s.windowsBaseSnapshotter.Usage(ctx, key)
	if err != nil {
		return snapshots.Usage{}, err
	}

	ctx, t, err := s.ms.TransactionContext(ctx, false)
	if err != nil {
		return snapshots.Usage{}, err
	}
	defer t.Rollback()

	id, _, _, err := storage.GetInfo(ctx, key)
	if err != nil {
		return snapshots.Usage{}, fmt.Errorf("failed to get snapshot info: %w", err)
	}

	if ok, err := s.isCimLayer(ctx, key); err != nil {
		return snapshots.Usage{}, err
	} else if ok {
		cimUsage, err := cimfs.GetCimUsage(ctx, getCimLayerPath(s.cimDir, id))
		if err != nil {
			return snapshots.Usage{}, err
		}
		baseUsage.Size += int64(cimUsage)
	}
	return baseUsage, nil
}

func (s *cimFSSnapshotter) Prepare(ctx context.Context, key, parent string, opts ...snapshots.Opt) ([]mount.Mount, error) {
	m, err := s.createSnapshot(ctx, snapshots.KindActive, key, parent, opts)
	if err != nil {
		return m, err
	}
	m[0].Type = "CimFS"
	return m, nil
}

func (s *cimFSSnapshotter) View(ctx context.Context, key, parent string, opts ...snapshots.Opt) ([]mount.Mount, error) {
	m, err := s.createSnapshot(ctx, snapshots.KindView, key, parent, opts)
	if err != nil {
		return m, err
	}
	m[0].Type = "CimFS"
	return m, nil
}

func (s *cimFSSnapshotter) Mounts(ctx context.Context, key string) ([]mount.Mount, error) {
	mounts, err := s.windowsBaseSnapshotter.Mounts(ctx, key)
	if err != nil {
		return nil, err
	}
	mounts[0].Type = "CimFS"
	return mounts, nil
}

func (s *cimFSSnapshotter) Commit(ctx context.Context, name, key string, opts ...snapshots.Opt) error {
	if !strings.Contains(key, snapshots.UnpackKeyPrefix) {
		return fmt.Errorf("committing a scratch snapshot to read-only cim layer isn't supported yet")
	}

	return s.ms.WithTransaction(ctx, true, func(ctx context.Context) error {
		usage, err := s.Usage(ctx, key)
		if err != nil {
			return fmt.Errorf("failed to get usage during commit: %w", err)
		}
		if _, err := storage.CommitActive(ctx, key, name, usage, opts...); err != nil {
			return fmt.Errorf("failed to commit snapshot: %w", err)
		}

		return nil
	})
}

// Remove abandons the transaction identified by key. All resources
// associated with the key will be removed.
func (s *cimFSSnapshotter) Remove(ctx context.Context, key string) error {
	var ID, renamedID string

	// collect original ID before preRemove
	err := s.ms.WithTransaction(ctx, false, func(ctx context.Context) error {
		var infoErr error
		ID, _, _, infoErr = storage.GetInfo(ctx, key)
		return infoErr
	})
	if err != nil {
		return fmt.Errorf("%w: failed to get snapshot info: %s", errdefs.ErrFailedPrecondition, err)
	}

	renamedID, err = s.preRemove(ctx, key)
	if err != nil {
		// wrap as ErrFailedPrecondition so that cleanup of other snapshots can continue
		return fmt.Errorf("%w: %s", errdefs.ErrFailedPrecondition, err)
	}

	if err := cimlayer.DestroyCimLayer(s.getSnapshotDir(ID)); err != nil {
		// Must be cleaned up, any "rm-*" could be removed if no active transactions
		log.G(ctx).WithError(err).WithField("ID", ID).Warnf("failed to cleanup cim files")
	}

	if err = hcsshim.DestroyLayer(s.info, renamedID); err != nil {
		// Must be cleaned up, any "rm-*" could be removed if no active transactions
		log.G(ctx).WithError(err).WithField("renamedID", renamedID).Warnf("failed to remove root filesystem")
	}
	return nil
}
