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
	"runtime"
	"syscall"
	"unsafe"
)

// There is no support of mount_setattr in golang
// "golang.org/x/sys/unix" package and even in glibc

type MountAttr struct {
	attr_set    uint64
	attr_clr    uint64
	propagation uint64
	userns      uint64
}

// AT_RECURSIVE is supported by glibc
// but doesn't supported by "golang.org/x/sys/unix"
const (
	AT_RECURSIVE = 0x8000
)

const (
	MOUNT_ATTR_RDONLY      = 0x00000001
	MOUNT_ATTR_NOSUID      = 0x00000002
	MOUNT_ATTR_NODEV       = 0x00000004
	MOUNT_ATTR_NOEXEC      = 0x00000008
	MOUNT_ATTR__ATIME      = 0x00000070
	MOUNT_ATTR_RELATIME    = 0x00000000
	MOUNT_ATTR_NOATIME     = 0x00000010
	MOUNT_ATTR_STRICTATIME = 0x00000020
	MOUNT_ATTR_NODIRATIME  = 0x00000080
	MOUNT_ATTR_IDMAP       = 0x00100000
)

func mountSetAttrSyscallNr() int {
	switch runtime.GOARCH {
	case "mips", "mipsle":
		return 4441
	case "mips64", "mips64le":
		return 5441
	default:
		return 441
	}
}

func MountSetAttr(dfd int, path string, flags uint, attr *MountAttr, size uint) (err error) {
	var _p0 *byte

	if _p0, err = syscall.BytePtrFromString(path); err != nil {
		return err
	}

	_, _, e1 := syscall.Syscall6(uintptr(mountSetAttrSyscallNr()), uintptr(dfd), uintptr(unsafe.Pointer(_p0)),
		uintptr(flags), uintptr(unsafe.Pointer(attr)), uintptr(size), 0)
	if e1 != 0 {
		err = e1
	}
	return
}
