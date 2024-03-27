//go:build linux
// +build linux

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

/*
Copyright 2013-2018 Docker, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	https://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package overlayutils

import (
	"path/filepath"
	"syscall"
)

// FsMagic unsigned id of the filesystem in use.
type FsMagic uint32

const (
	// FsMagicUnsupported is a predefined constant value other than a valid filesystem id.
	FsMagicUnsupported = FsMagic(0x00000000)
)

const (
	// FsMagicAufs filesystem id for Aufs
	FsMagicAufs = FsMagic(0x61756673)
	// FsMagicBtrfs filesystem id for Btrfs
	FsMagicBtrfs = FsMagic(0x9123683E)
	// FsMagicCramfs filesystem id for Cramfs
	FsMagicCramfs = FsMagic(0x28cd3d45)
	// FsMagicEcryptfs filesystem id for eCryptfs
	FsMagicEcryptfs = FsMagic(0xf15f)
	// FsMagicExtfs filesystem id for Extfs
	FsMagicExtfs = FsMagic(0x0000EF53)
	// FsMagicF2fs filesystem id for F2fs
	FsMagicF2fs = FsMagic(0xF2F52010)
	// FsMagicGPFS filesystem id for GPFS
	FsMagicGPFS = FsMagic(0x47504653)
	// FsMagicJffs2Fs filesystem if for Jffs2Fs
	FsMagicJffs2Fs = FsMagic(0x000072b6)
	// FsMagicJfs filesystem id for Jfs
	FsMagicJfs = FsMagic(0x3153464a)
	// FsMagicNfsFs filesystem id for NfsFs
	FsMagicNfsFs = FsMagic(0x00006969)
	// FsMagicRAMFs filesystem id for RamFs
	FsMagicRAMFs = FsMagic(0x858458f6)
	// FsMagicReiserFs filesystem id for ReiserFs
	FsMagicReiserFs = FsMagic(0x52654973)
	// FsMagicSmbFs filesystem id for SmbFs
	FsMagicSmbFs = FsMagic(0x0000517B)
	// FsMagicSquashFs filesystem id for SquashFs
	FsMagicSquashFs = FsMagic(0x73717368)
	// FsMagicTmpFs filesystem id for TmpFs
	FsMagicTmpFs = FsMagic(0x01021994)
	// FsMagicVxFS filesystem id for VxFs
	FsMagicVxFS = FsMagic(0xa501fcf5)
	// FsMagicXfs filesystem id for Xfs
	FsMagicXfs = FsMagic(0x58465342)
	// FsMagicZfs filesystem id for Zfs
	FsMagicZfs = FsMagic(0x2fc12fc1)
	// FsMagicOverlay filesystem id for overlay
	FsMagicOverlay = FsMagic(0x794C7630)
)

var (
	// FsNames maps filesystem id to name of the filesystem.
	FsNames = map[FsMagic]string{
		FsMagicAufs:        "aufs",
		FsMagicBtrfs:       "btrfs",
		FsMagicCramfs:      "cramfs",
		FsMagicExtfs:       "extfs",
		FsMagicF2fs:        "f2fs",
		FsMagicGPFS:        "gpfs",
		FsMagicJffs2Fs:     "jffs2",
		FsMagicJfs:         "jfs",
		FsMagicNfsFs:       "nfs",
		FsMagicOverlay:     "overlayfs",
		FsMagicRAMFs:       "ramfs",
		FsMagicReiserFs:    "reiserfs",
		FsMagicSmbFs:       "smb",
		FsMagicSquashFs:    "squashfs",
		FsMagicTmpFs:       "tmpfs",
		FsMagicUnsupported: "unsupported",
		FsMagicVxFS:        "vxfs",
		FsMagicXfs:         "xfs",
		FsMagicZfs:         "zfs",
	}
)

// GetFSMagic returns the filesystem id given the path.
func GetFSMagic(rootpath string) (FsMagic, error) {
	var buf syscall.Statfs_t
	if err := syscall.Statfs(filepath.Dir(rootpath), &buf); err != nil {
		return 0, err
	}
	return FsMagic(buf.Type), nil
}
