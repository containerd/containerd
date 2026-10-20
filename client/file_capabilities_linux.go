//go:build linux

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

package client

import (
	"errors"
	"fmt"

	"github.com/containerd/containerd/v2/internal/userns"
	"github.com/containerd/containerd/v2/pkg/cap"
	"github.com/containerd/containerd/v2/pkg/kernelversion"
	"github.com/containerd/continuity/sysx"
	"golang.org/x/sys/unix"
)

const fileCapabilityXattr = "security.capability"

func supportsNamespacedFileCapabilities() (bool, error) {
	return kernelversion.GreaterEqualThan(kernelversion.KernelVersion{Kernel: 4, Major: 14})
}

// Missing/unsupported xattrs are tolerated on read. Older kernels and valid
// unmapped roots retain stripping with a warning; all other failures are fatal.
func remappedFileCapabilities(path string, idMap userns.IDMap, warn func(string)) ([]byte, error) {
	supported, err := supportsNamespacedFileCapabilities()
	if err != nil {
		return nil, fmt.Errorf("check namespaced file capability support: %w", err)
	}
	if !supported {
		// Older kernels can store revision 3 xattrs but fail to execute the
		// file. Retain the previous behavior of letting Lchown clear them.
		warn("namespaced file capabilities require Linux 4.14")
		return nil, nil
	}
	caps, err := sysx.LGetxattr(path, fileCapabilityXattr)
	if errors.Is(err, unix.ENODATA) || errors.Is(err, unix.EOPNOTSUPP) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read file capabilities for %q: %w", path, err)
	}
	caps, err = remapFileCapabilities(caps, idMap)
	if err != nil {
		return nil, fmt.Errorf("remap file capabilities for %q: %w", path, err)
	}
	if caps == nil {
		warn("file capability root is outside the UID mapping")
	}
	return caps, nil
}

// remapFileCapabilities scopes the saved capabilities to their original root
// UID translated into the destination user namespace.
func remapFileCapabilities(data []byte, idMap userns.IDMap) ([]byte, error) {
	caps, err := cap.ParseFileCapabilities(data)
	if err != nil {
		return nil, err
	}
	// Capability root IDs are UIDs; the GID mapping is unrelated.
	uidMap := userns.IDMap{UidMap: idMap.UidMap}
	root, err := uidMap.ToHost(userns.User{Uid: caps.RootID})
	if err != nil {
		if caps.Namespaced() && errors.Is(err, userns.ErrUnmappedID) {
			return nil, nil // Let Lchown clear capabilities whose root cannot be mapped.
		}
		return nil, err
	}
	caps.RootID = root.Uid
	return caps.Encode(), nil
}

// Restore failures remain fatal, including unsupported writes.
func restoreFileCapabilities(path string, caps []byte) error {
	if caps == nil {
		return nil
	}
	if err := sysx.LSetxattr(path, fileCapabilityXattr, caps, 0); err != nil {
		return fmt.Errorf("restore file capabilities for %q: %w", path, err)
	}
	return nil
}
