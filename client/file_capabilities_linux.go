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
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/containerd/containerd/v2/internal/userns"
	"github.com/containerd/containerd/v2/pkg/kernelversion"
	"github.com/containerd/continuity/sysx"
	"golang.org/x/sys/unix"
)

// Linux UAPI: include/uapi/linux/capability.h.
const (
	fileCapabilityXattr = "security.capability"
	capRevisionMask     = 0xff000000
	capRevision1        = 0x01000000
	capRevision2        = 0x02000000
	capRevision3        = 0x03000000
	capEffective        = 1
	capSize1            = 12
	capSize2            = 20
	capSize3            = 24
)

func supportsNamespacedFileCapabilities() (bool, error) {
	return kernelversion.GreaterEqualThan(kernelversion.KernelVersion{Kernel: 4, Major: 14})
}

func remappedFileCapabilities(path string, idMap userns.IDMap) ([]byte, error) {
	supported, err := supportsNamespacedFileCapabilities()
	if err != nil {
		return nil, fmt.Errorf("check namespaced file capability support: %w", err)
	}
	if !supported {
		// Older kernels can store revision 3 xattrs but fail to execute the
		// file. Retain the previous behavior of letting Lchown clear them.
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
	return caps, nil
}

// remapFileCapabilities scopes the saved capabilities to their original root
// UID translated into the destination user namespace. Restoring a revision 2
// xattr verbatim would leave it scoped to host root, not the remapped root.
func remapFileCapabilities(caps []byte, idMap userns.IDMap) ([]byte, error) {
	if len(caps) < 4 {
		return nil, fmt.Errorf("invalid file capability length %d", len(caps))
	}
	magic := binary.LittleEndian.Uint32(caps)
	revision := magic & capRevisionMask
	var size int
	var rootUID uint32
	switch revision {
	case capRevision1:
		size = capSize1
	case capRevision2:
		size = capSize2
	case capRevision3:
		size = capSize3
	default:
		return nil, fmt.Errorf("unsupported file capability revision %#x", revision)
	}
	if len(caps) != size || magic & ^uint32(capRevisionMask|capEffective) != 0 {
		return nil, errors.New("invalid file capability encoding")
	}
	if revision == capRevision3 {
		rootUID = binary.LittleEndian.Uint32(caps[capSize2:capSize3])
	}
	// Capability root IDs are UIDs; the GID mapping is unrelated.
	uidMap := userns.IDMap{UidMap: idMap.UidMap}
	root, err := uidMap.ToHost(userns.User{Uid: rootUID})
	if err != nil {
		if revision != capRevision3 || rootUID == ^uint32(0) || len(idMap.UidMap) == 0 {
			return nil, err
		}
		for _, m := range idMap.UidMap {
			if m.Size == 0 || uint64(m.ContainerID)+uint64(m.Size) > uint64(^uint32(0)) ||
				uint64(m.HostID)+uint64(m.Size) > uint64(^uint32(0)) ||
				(rootUID >= m.ContainerID && rootUID-m.ContainerID < m.Size) {
				return nil, err
			}
		}
		// This capability's root cannot be represented in the remapped
		// namespace. Let Lchown clear it instead of preventing container creation.
		return nil, nil
	}
	mapped := make([]byte, capSize3)
	copy(mapped, caps)
	binary.LittleEndian.PutUint32(mapped, capRevision3|(magic&capEffective))
	binary.LittleEndian.PutUint32(mapped[capSize2:capSize3], root.Uid)
	return mapped, nil
}

func restoreFileCapabilities(path string, caps []byte) error {
	if caps == nil {
		return nil
	}
	if err := sysx.LSetxattr(path, fileCapabilityXattr, caps, 0); err != nil {
		return fmt.Errorf("restore file capabilities for %q: %w", path, err)
	}
	return nil
}
