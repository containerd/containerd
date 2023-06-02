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

package cimfs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/Microsoft/hcsshim/pkg/cimfs"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/mountmanager"
	"github.com/containerd/typeurl/v2"
	bolt "go.etcd.io/bbolt"
)

const PluginName = "mount-cimfs"

func init() {
	const prefix = "types.containerd.io/mountmanager/cimfs"
	typeurl.Register(&CimMountRequest{}, prefix, "mountrequest")
	typeurl.Register(&CimMountResponse{}, prefix, "mountresponse")
	typeurl.Register(&CimUnmountRequest{}, prefix, "unmountrequest")
	typeurl.Register(&CimUnmountResponse{}, prefix, "unmountresponse")
}

type CimMountRequest struct {
	CimPath string
}

type CimMountResponse struct {
	Volume string
}

type CimUnmountRequest struct {
	CimPath string
}

type CimUnmountResponse struct{}

type cimfsMountManager struct {
	opLock sync.Mutex
	s      *store
}

var _ mountmanager.MountManager = &cimfsMountManager{}

var (
	ErrCimNotMounted = fmt.Errorf("cim not mounted")
)

func NewCimfsMountManager(rootdir string) (mountmanager.MountManager, error) {
	if err := os.MkdirAll(rootdir, 066); err != nil {
		return nil, fmt.Errorf("create plugin directory: %w", err)
	}
	s, err := newStore(filepath.Join(rootdir, "cimfsmountmanager.db"))
	if err != nil {
		return nil, fmt.Errorf("create persistence store: %w", err)
	}
	return &cimfsMountManager{
		s: s,
	}, nil
}

func (mm *cimfsMountManager) Close() {
	mm.s.Close()
}

func (mm *cimfsMountManager) mountRefCounted(ctx context.Context, cimPath string) (mountedVol string, err error) {
	mm.opLock.Lock()
	defer mm.opLock.Unlock()

	cinfo, err := mm.s.getCimMountInfo(cimPath)
	if err != nil && !errors.Is(err, bolt.ErrBucketNotFound) {
		return "", fmt.Errorf("couldn't check if cim %s is already mounted: %w", cimPath, err)
	} else if errors.Is(err, bolt.ErrBucketNotFound) {
		// mount the cim
		mountedVol, err = cimfs.Mount(cimPath)
		if err != nil {
			return "", err
		}
		log.G(ctx).Debugf("cim %s mounted at %s", cimPath, mountedVol)
		defer func() {
			if err != nil {
				cimfs.Unmount(mountedVol)
			}
		}()
	} else {
		mountedVol = cinfo.volume
	}

	if err = mm.s.recordCimMount(cimPath, mountedVol); err != nil {
		return "", err
	}
	return mountedVol, nil
}

func (mm *cimfsMountManager) unmountRefCounted(ctx context.Context, cimPath string) error {
	mm.opLock.Lock()
	defer mm.opLock.Unlock()

	cinfo, err := mm.s.getCimMountInfo(cimPath)
	if errors.Is(err, bolt.ErrBucketNotFound) {
		return nil
	} else if err != nil {
		return fmt.Errorf("couldn't check if cim %s is already mount: %w", cimPath, err)
	}

	if err = mm.s.recordCimUnmount(cimPath); err != nil {
		return err
	}

	if cinfo.refCount == 1 {
		if err := cimfs.Unmount(cinfo.volume); err != nil {
			log.G(ctx).WithError(err).Debugf("cim %s unmount", cimPath)
			return err
		}
		log.G(ctx).Debugf("cim %s unmounted", cimPath)
	}
	return nil
}

func (mm *cimfsMountManager) getMountPath(ctx context.Context, cimPath string) (string, error) {
	cinfo, err := mm.s.getCimMountInfo(cimPath)
	if errors.Is(err, bolt.ErrBucketNotFound) {
		return "", ErrCimNotMounted
	} else if err != nil {
		return "", fmt.Errorf("failed to get cim mount path: %w", err)
	}
	return cinfo.volume, nil
}

func (mm *cimfsMountManager) Mount(ctx context.Context, data interface{}) (interface{}, error) {
	mountReq, ok := data.(*CimMountRequest)
	if !ok {
		return nil, errdefs.ToGRPC(fmt.Errorf("invalid request type %T", data))
	}

	mountedVol, err := mm.mountRefCounted(ctx, mountReq.CimPath)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}

	return &CimMountResponse{
		Volume: mountedVol,
	}, nil
}

func (mm *cimfsMountManager) Unmount(ctx context.Context, data interface{}) (interface{}, error) {
	umountReq, ok := data.(*CimUnmountRequest)
	if !ok {
		return nil, errdefs.ToGRPC(fmt.Errorf("invalid request type %T", data))
	}

	if err := mm.unmountRefCounted(ctx, umountReq.CimPath); err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	return &CimUnmountResponse{}, nil
}
