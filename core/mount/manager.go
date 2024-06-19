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

package mount

import (
	"context"
	"time"
)

// MountManager handles activating a mount array to be mounted by the
// system. It supports custom mount types that can be handled by
// plugins and don't need to be directly mountable. For example, this
// can be used to do device activation and setting up process or
// sockets such as for fuse or tcmu.
// The returned activation info will contain the remaining mounts
// which must be performed by the system, likely in a container's
// mount namespace. Any mounts or devices activated by the mount
// manager will be done outside the container's namespace.
type MountManager interface {
	Activate(context.Context, string, []Mount, ...ActivateOpt) (ActivationInfo, error)
	Deactivate(context.Context, string) error
	Info(context.Context, string) (ActivationInfo, error)
	Update(context.Context, ActivationInfo, ...string) (ActivationInfo, error)
	List(context.Context, ...string) ([]ActivationInfo, error)
}

// MountHandler is an interface for plugins to perform a mount which is managed
// by a MountManager. The MountManager will will be responsible for associating
// mount types to MountHandlers and determining what the plugin should be used.
// The MountHandler interface is intended to be used for custom mount plugins
// and does not replace the mount calls for system mounts.
type MountHandler interface {
	Mount(context.Context, Mount, string, []ActiveMount) (ActiveMount, error)
	Unmount(context.Context, string) error
}

type ActivateOptions struct {
	// Mount type: native, filesystem, block device

	// Labels are the labels to use for the activation
	Labels map[string]string

	// TempMount specifies that the mount will be used temporarily
	// and all mounts should be performed
	TempMount bool

	// Final target with option to perform all mounts, normally the runtime will perform the root filesystem mount
	// Custom temp directory for temporary mounts (for example devices mounted and used as overlay lowers)
}

type ActivateOpt func(*ActivateOptions)

// ActiveMount represents a mount which has been mounted by a
// MountHandler or directly mounted by a mount manager.
type ActiveMount struct {
	Mount
	MountedAt *time.Time

	// MountPoint is the filesystem mount location
	MountPoint string

	// MountData is metadata used by the mount type which can also be used by
	// subsequent mounts.
	MountData map[string]string
}

// ActivationInfo represents the state of an active set of mounts being managed by a
// mount manager. The Name is unique and can be used to reference the activation
// from other resources.
type ActivationInfo struct {
	Name string

	// Active are the mounts which was successfully moounted on activate
	Active []ActiveMount

	// System is the list of system mounts to access the filesystem root
	// This will always be non-empty and a bind mount will be created
	// and filled in here when all mounts are performed
	System []Mount
	Labels map[string]string
}
