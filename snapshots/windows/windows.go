// +build windows

package windows

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"github.com/Microsoft/hcsshim"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/fs"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/snapshots"
	"github.com/containerd/containerd/snapshots/storage"
	"github.com/pkg/errors"
	"golang.org/x/sys/windows"
)

func init() {
	plugin.Register(&plugin.Registration{
		Type: plugin.SnapshotPlugin,
		ID:   "windows",
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			return NewSnapshotter(ic.Root)
		},
	})
}

type snapshotter struct {
	root string
	info hcsshim.DriverInfo
	ms   *storage.MetaStore
}

// NewSnapshotter returns a new windows snapshotter
func NewSnapshotter(root string) (snapshots.Snapshotter, error) {
	fsType, err := getFileSystemType(string(root[0]))
	if err != nil {
		return nil, err
	}
	if strings.ToLower(fsType) == "refs" {
		return nil, errors.Wrapf(errdefs.ErrInvalidArgument, "%s is on an ReFS volume - ReFS volumes are not supported", root)
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
	if err != nil {
		return snapshots.Info{}, err
	}

	return info, nil
}

func (s *snapshotter) Update(ctx context.Context, info snapshots.Info, fieldpaths ...string) (snapshots.Info, error) {
	ctx, t, err := s.ms.TransactionContext(ctx, true)
	if err != nil {
		return snapshots.Info{}, err
	}

	var committed bool
	defer func() {
		if committed == false {
			rollbackWithLogging(ctx, t)
		}
	}()

	info, err = storage.UpdateInfo(ctx, info, fieldpaths...)
	if err != nil {
		return snapshots.Info{}, err
	}

	if err := t.Commit(); err != nil {
		return snapshots.Info{}, err
	}
	committed = true

	return info, nil
}

func (s *snapshotter) Usage(ctx context.Context, key string) (snapshots.Usage, error) {
	ctx, t, err := s.ms.TransactionContext(ctx, false)
	if err != nil {
		return snapshots.Usage{}, err
	}
	defer t.Rollback()

	_, info, usage, err := storage.GetInfo(ctx, key)
	if err != nil {
		return snapshots.Usage{}, err
	}

	if info.Kind == snapshots.KindActive {
		du := fs.Usage{
			Size: 0,
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

	var committed bool
	defer func() {
		if committed == false {
			rollbackWithLogging(ctx, t)
		}
	}()
	usage := fs.Usage{
		Size: 0,
	}

	if _, err = storage.CommitActive(ctx, key, name, snapshots.Usage(usage), opts...); err != nil {
		return errors.Wrap(err, "failed to commit snapshot")
	}

	if err := t.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

// Remove abandons the transaction identified by key. All resources
// associated with the key will be removed.
func (s *snapshotter) Remove(ctx context.Context, key string) error {
	ctx, t, err := s.ms.TransactionContext(ctx, true)
	if err != nil {
		return err
	}

	var committed bool
	defer func() {
		if committed == false {
			rollbackWithLogging(ctx, t)
		}
	}()

	id, _, err := storage.Remove(ctx, key)
	if err != nil {
		return errors.Wrap(err, "failed to remove")
	}

	path := s.getSnapshotDir(id)
	renamedID := "rm-" + id
	renamed := filepath.Join(s.root, "snapshots", "rm-"+id)
	if err := os.Rename(path, renamed); err != nil && !os.IsNotExist(err) {
		return err
	}

	err = t.Commit()
	if err != nil {
		if err1 := os.Rename(renamed, path); err1 != nil {
			// May cause inconsistent data on disk
			log.G(ctx).WithError(err1).WithField("path", renamed).Errorf("Failed to rename after failed commit")
		}
		return errors.Wrap(err, "failed to commit")
	}
	committed = true

	if err := hcsshim.DestroyLayer(s.info, renamedID); err != nil {
		// Must be cleaned up, any "rm-*" could be removed if no active transactions
		log.G(ctx).WithError(err).WithField("path", renamed).Warnf("Failed to remove root filesystem")
	}

	return nil
}

// Walk the committed snapshots.
func (s *snapshotter) Walk(ctx context.Context, fn func(context.Context, snapshots.Info) error) error {
	ctx, t, err := s.ms.TransactionContext(ctx, false)
	if err != nil {
		return err
	}
	defer t.Rollback()
	return storage.WalkInfo(ctx, fn)
}

// Close closes the snapshotter
func (s *snapshotter) Close() error {
	return s.ms.Close()
}

func (s *snapshotter) mounts(sn storage.Snapshot) []mount.Mount {
	var (
		roFlag string
	)

	if sn.Kind == snapshots.KindView {
		roFlag = "ro"
	} else {
		roFlag = "rw"
	}

	parentLayerPaths := s.parentIDsToParentPaths(sn.ParentIDs)
	// error is not checked here, as a string array will never fail to Marshal
	parentLayersJSON, _ := json.Marshal(parentLayerPaths)
	parentLayersOption := mount.ParentLayerPathsFlag + string(parentLayersJSON)

	var mounts []mount.Mount
	mounts = append(mounts, mount.Mount{
		Source: s.getSnapshotDir(sn.ID),
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

	var committed bool
	defer func() {
		if committed == false {
			rollbackWithLogging(ctx, t)
		}
	}()

	newSnapshot, err := storage.CreateSnapshot(ctx, kind, key, parent, opts...)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create snapshot")
	}

	switch kind {
	case snapshots.KindView:
		var parentID string
		if len(newSnapshot.ParentIDs) != 0 {
			parentID = newSnapshot.ParentIDs[0]
		}
		if err := hcsshim.CreateLayer(s.info, newSnapshot.ID, parentID); err != nil {
			return nil, errors.Wrap(err, "failed to create layer")
		}
	case snapshots.KindActive:
		parentLayerPaths := s.parentIDsToParentPaths(newSnapshot.ParentIDs)

		var parentPath string
		if len(parentLayerPaths) != 0 {
			parentPath = parentLayerPaths[0]
		}

		if err := hcsshim.CreateSandboxLayer(s.info, newSnapshot.ID, parentPath, parentLayerPaths); err != nil {
			return nil, errors.Wrap(err, "failed to create sandbox layer")
		}

		// TODO(darrenstahlmsft): Allow changing sandbox size
	}

	if err := t.Commit(); err != nil {
		return nil, errors.Wrap(err, "commit failed")
	}
	committed = true

	return s.mounts(newSnapshot), nil
}

func (s *snapshotter) parentIDsToParentPaths(parentIDs []string) []string {
	var parentLayerPaths []string
	for _, ID := range parentIDs {
		parentLayerPaths = append(parentLayerPaths, s.getSnapshotDir(ID))
	}
	return parentLayerPaths
}

// getFileSystemType obtains the type of a file system through GetVolumeInformation
// https://msdn.microsoft.com/en-us/library/windows/desktop/aa364993(v=vs.85).aspx
func getFileSystemType(drive string) (fsType string, hr error) {
	var (
		modkernel32              = windows.NewLazySystemDLL("kernel32.dll")
		procGetVolumeInformation = modkernel32.NewProc("GetVolumeInformationW")
		buf                      = make([]uint16, 255)
		size                     = windows.MAX_PATH + 1
	)
	if len(drive) != 1 {
		return "", errors.New("getFileSystemType must be called with a drive letter")
	}
	drive += `:\`
	n := uintptr(unsafe.Pointer(nil))
	r0, _, _ := syscall.Syscall9(procGetVolumeInformation.Addr(), 8, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(drive))), n, n, n, n, n, uintptr(unsafe.Pointer(&buf[0])), uintptr(size), 0)
	if int32(r0) < 0 {
		hr = syscall.Errno(win32FromHresult(r0))
	}
	fsType = windows.UTF16ToString(buf)
	return
}

// win32FromHresult is a helper function to get the win32 error code from an HRESULT
func win32FromHresult(hr uintptr) uintptr {
	if hr&0x1fff0000 == 0x00070000 {
		return hr & 0xffff
	}
	return hr
}
