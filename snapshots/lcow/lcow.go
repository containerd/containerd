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

package lcow

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
	"syscall"
	"time"
	"unsafe"

	"github.com/Microsoft/hcsshim/pkg/go-runhcs"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/snapshots"
	"github.com/containerd/containerd/snapshots/storage"
	"github.com/containerd/continuity/fs"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"golang.org/x/sys/windows"
)

func init() {
	plugin.Register(&plugin.Registration{
		Type: plugin.SnapshotPlugin,
		ID:   "windows-lcow",
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			ic.Meta.Platforms = append(ic.Meta.Platforms, ocispec.Platform{
				OS:           "linux",
				Architecture: "amd64",
			})
			return NewSnapshotter(ic.Root)
		},
	})
}

type snapshotter struct {
	root string
	ms   *storage.MetaStore

	scratchLock sync.Mutex
}

// NewSnapshotter returns a new windows snapshotter
func NewSnapshotter(root string) (snapshots.Snapshotter, error) {
	fsType, err := getFileSystemType(root)
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
	log.G(ctx).Debug("Starting Stat")
	ctx, t, err := s.ms.TransactionContext(ctx, false)
	if err != nil {
		return snapshots.Info{}, err
	}
	defer t.Rollback()

	_, info, _, err := storage.GetInfo(ctx, key)
	return info, err
}

func (s *snapshotter) Update(ctx context.Context, info snapshots.Info, fieldpaths ...string) (snapshots.Info, error) {
	log.G(ctx).Debug("Starting Update")
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
	log.G(ctx).Debug("Starting Usage")
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
	log.G(ctx).Debug("Starting Prepare")
	return s.createSnapshot(ctx, snapshots.KindActive, key, parent, opts)
}

func (s *snapshotter) View(ctx context.Context, key, parent string, opts ...snapshots.Opt) ([]mount.Mount, error) {
	log.G(ctx).Debug("Starting View")
	return s.createSnapshot(ctx, snapshots.KindView, key, parent, opts)
}

// Mounts returns the mounts for the transaction identified by key. Can be
// called on an read-write or readonly transaction.
//
// This can be used to recover mounts after calling View or Prepare.
func (s *snapshotter) Mounts(ctx context.Context, key string) ([]mount.Mount, error) {
	log.G(ctx).Debug("Starting Mounts")
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
	log.G(ctx).Debug("Starting Commit")
	ctx, t, err := s.ms.TransactionContext(ctx, true)
	if err != nil {
		return err
	}
	defer t.Rollback()

	usage := fs.Usage{
		Size: 0,
	}

	if _, err = storage.CommitActive(ctx, key, name, snapshots.Usage(usage), opts...); err != nil {
		return errors.Wrap(err, "failed to commit snapshot")
	}

	if err := t.Commit(); err != nil {
		return err
	}
	return nil
}

// Remove abandons the transaction identified by key. All resources
// associated with the key will be removed.
func (s *snapshotter) Remove(ctx context.Context, key string) error {
	log.G(ctx).Debug("Starting Remove")
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
	renamed := s.getSnapshotDir("rm-" + id)
	if err := os.Rename(path, renamed); err != nil && !os.IsNotExist(err) {
		return err
	}

	if err := t.Commit(); err != nil {
		if err1 := os.Rename(renamed, path); err1 != nil {
			// May cause inconsistent data on disk
			log.G(ctx).WithError(err1).WithField("path", renamed).Errorf("Failed to rename after failed commit")
		}
		return errors.Wrap(err, "failed to commit")
	}

	if err := os.RemoveAll(renamed); err != nil {
		// Must be cleaned up, any "rm-*" could be removed if no active transactions
		log.G(ctx).WithError(err).WithField("path", renamed).Warnf("Failed to remove root filesystem")
	}

	return nil
}

// Walk the committed snapshots.
func (s *snapshotter) Walk(ctx context.Context, fn func(context.Context, snapshots.Info) error) error {
	log.G(ctx).Debug("Starting Walk")
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
		Type:   "lcow-layer",
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

		scratchSource, err := s.openOrCreateScratch(ctx)
		if err != nil {
			return nil, err
		}
		defer scratchSource.Close()

		// TODO: JTERRY75 - This has to be called sandbox.vhdx for the time
		// being but it really is the scratch.vhdx Using this naming convention
		// for now but this is not the kubernetes sandbox.
		//
		// Create the sandbox.vhdx for this snapshot from the cache.
		destPath := filepath.Join(snDir, "sandbox.vhdx")
		dest, err := os.OpenFile(destPath, os.O_RDWR|os.O_CREATE, 0700)
		if err != nil {
			return nil, errors.Wrap(err, "failed to create sandbox.vhdx in snapshot")
		}
		defer dest.Close()
		if _, err := io.Copy(dest, scratchSource); err != nil {
			dest.Close()
			os.Remove(destPath)
			return nil, errors.Wrap(err, "failed to copy cached scratch.vhdx to sandbox.vhdx in snapshot")
		}
	}

	if err := t.Commit(); err != nil {
		return nil, errors.Wrap(err, "commit failed")
	}

	return s.mounts(newSnapshot), nil
}

func (s *snapshotter) openOrCreateScratch(ctx context.Context) (_ *os.File, err error) {
	// Create the scratch.vhdx cache file if it doesn't already exit.
	s.scratchLock.Lock()
	defer s.scratchLock.Unlock()

	scratchFinalPath := filepath.Join(s.root, "scratch.vhdx")
	scratchSource, err := os.OpenFile(scratchFinalPath, os.O_RDONLY, 0700)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, errors.Wrap(err, "failed to open scratch.vhdx for read")
		}

		log.G(ctx).Debug("scratch.vhdx not found, creating a new one")

		// Golang logic for ioutil.TempFile without the file creation
		r := uint32(time.Now().UnixNano() + int64(os.Getpid()))
		r = r*1664525 + 1013904223 // constants from Numerical Recipes

		scratchTempName := fmt.Sprintf("scratch-%s-tmp.vhdx", strconv.Itoa(int(1e9 + r%1e9))[1:])
		scratchTempPath := filepath.Join(s.root, scratchTempName)

		// Create the scratch
		rhcs := runhcs.Runhcs{
			Debug:     true,
			Log:       filepath.Join(s.root, "runhcs-scratch.log"),
			LogFormat: runhcs.JSON,
			Owner:     "containerd",
		}
		if err := rhcs.CreateScratch(ctx, scratchTempPath); err != nil {
			_ = os.Remove(scratchTempPath)
			return nil, errors.Wrapf(err, "failed to create '%s' temp file", scratchTempName)
		}
		if err := os.Rename(scratchTempPath, scratchFinalPath); err != nil {
			_ = os.Remove(scratchTempPath)
			return nil, errors.Wrapf(err, "failed to rename '%s' temp file to 'scratch.vhdx'", scratchTempName)
		}
		scratchSource, err = os.OpenFile(scratchFinalPath, os.O_RDONLY, 0700)
		if err != nil {
			_ = os.Remove(scratchFinalPath)
			return nil, errors.Wrap(err, "failed to open scratch.vhdx for read after creation")
		}
	}
	return scratchSource, nil
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
func getFileSystemType(path string) (fsType string, hr error) {
	drive := filepath.VolumeName(path)
	if len(drive) != 2 {
		return "", errors.New("getFileSystemType path must start with a drive letter")
	}

	var (
		modkernel32              = windows.NewLazySystemDLL("kernel32.dll")
		procGetVolumeInformation = modkernel32.NewProc("GetVolumeInformationW")
		buf                      = make([]uint16, 255)
		size                     = windows.MAX_PATH + 1
	)
	drive += `\`
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
