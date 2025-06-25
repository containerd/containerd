// Copyright 2016 the Go-FUSE Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fuse

import (
	"fmt"
	"runtime"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

var statxFieldFlags = newFlagNames([]flagNameEntry{
	{unix.STATX_ATIME, "Atime"},
	{unix.STATX_BLOCKS, "blocks"},
	{unix.STATX_BTIME, "Btime"},
	{unix.STATX_CTIME, "Ctime"},
	{unix.STATX_GID, "Gid"},
	{unix.STATX_INO, "Ino"},
	{unix.STATX_MNT_ID, "Mntid"},
	{unix.STATX_MODE, "Mode"},
	{unix.STATX_MTIME, "Mtime"},
	{unix.STATX_NLINK, "Nlink"},
	{unix.STATX_SIZE, "Size"},
	{unix.STATX_TYPE, "Type"},
	{unix.STATX_UID, "Uid"},
})

func init() {
	// syscall.O_LARGEFILE is 0x0 on x86_64, but the kernel
	// supplies 0x8000 anyway, except on mips64el, where 0x8000 is
	// used for O_DIRECT.
	if !strings.Contains(runtime.GOARCH, "mips64") {
		openFlagNames.set(0x8000, "LARGEFILE")
	}

	openFlagNames.set(syscall.O_DIRECT, "DIRECT")
	openFlagNames.set(syscall_O_NOATIME, "NOATIME")
	initFlagNames.set(CAP_NO_OPENDIR_SUPPORT, "NO_OPENDIR_SUPPORT")
	initFlagNames.set(CAP_EXPLICIT_INVAL_DATA, "EXPLICIT_INVAL_DATA")
	initFlagNames.set(CAP_MAP_ALIGNMENT, "MAP_ALIGNMENT")
	initFlagNames.set(CAP_SUBMOUNTS, "SUBMOUNTS")
	initFlagNames.set(CAP_HANDLE_KILLPRIV_V2, "HANDLE_KILLPRIV_V2")
	initFlagNames.set(CAP_SETXATTR_EXT, "SETXATTR_EXT")
	initFlagNames.set(CAP_INIT_EXT, "INIT_EXT")
	initFlagNames.set(CAP_INIT_RESERVED, "INIT_RESERVED")
}

func (a *Statx) string() string {
	var ss []string
	if a.Mask&unix.STATX_MODE != 0 || a.Mask&unix.STATX_TYPE != 0 {
		ss = append(ss, fmt.Sprintf("M0%o", a.Mode))
	}
	if a.Mask&unix.STATX_SIZE != 0 {
		ss = append(ss, fmt.Sprintf("SZ=%d", a.Size))
	}
	if a.Mask&unix.STATX_NLINK != 0 {
		ss = append(ss, fmt.Sprintf("L=%d", a.Nlink))
	}
	if a.Mask&unix.STATX_UID != 0 || a.Mask&unix.STATX_GID != 0 {
		ss = append(ss, fmt.Sprintf("%d:%d", a.Uid, a.Gid))
	}
	if a.Mask&unix.STATX_INO != 0 {
		ss = append(ss, fmt.Sprintf("i%d", a.Ino))
	}
	if a.Mask&unix.STATX_ATIME != 0 {
		ss = append(ss, fmt.Sprintf("A %f", a.Atime.Seconds()))
	}
	if a.Mask&unix.STATX_BTIME != 0 {
		ss = append(ss, fmt.Sprintf("B %f", a.Btime.Seconds()))
	}
	if a.Mask&unix.STATX_CTIME != 0 {
		ss = append(ss, fmt.Sprintf("C %f", a.Ctime.Seconds()))
	}
	if a.Mask&unix.STATX_MTIME != 0 {
		ss = append(ss, fmt.Sprintf("M %f", a.Mtime.Seconds()))
	}
	if a.Mask&unix.STATX_BLOCKS != 0 {
		ss = append(ss, fmt.Sprintf("%d*%d", a.Blocks, a.Blksize))
	}

	return "{" + strings.Join(ss, " ") + "}"
}

func (in *StatxIn) string() string {
	return fmt.Sprintf("{Fh %d %s 0x%x %s}", in.Fh, flagString(getAttrFlagNames, int64(in.GetattrFlags), ""),
		in.SxFlags, flagString(statxFieldFlags, int64(in.SxMask), ""))
}
