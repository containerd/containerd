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

package cimfs

import (
	"context"

	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/mountmanager"
	"github.com/containerd/typeurl/v2"
)

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

// TODO(ambarve): Right now this plugin is only added to show the entire workflow. Once the mountmanager APIs
// are finalized and that PR is merged, this will be updated to do the actual mount/unmount
type cimfsMountManager struct {
}

var _ mountmanager.MountManager = &cimfsMountManager{}

func NewCimfsMountManager() (mountmanager.MountManager, error) {
	return &cimfsMountManager{}, nil
}

// Mount implements mountmanager.MountManager
func (*cimfsMountManager) Mount(ctx context.Context, data interface{}) (interface{}, error) {
	return nil, errdefs.ErrNotImplemented
}

// Unmount implements mountmanager.MountManager
func (*cimfsMountManager) Unmount(ctx context.Context, data interface{}) (interface{}, error) {
	return nil, errdefs.ErrNotImplemented
}
