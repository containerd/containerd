// Copyright 2016 the Go-FUSE Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fuse

import (
	"syscall"

	"golang.org/x/sys/unix"
)

func (a *Attr) FromStat(s *syscall.Stat_t) {
	a.Ino = uint64(s.Ino)
	a.Size = uint64(s.Size)
	a.Blocks = uint64(s.Blocks)
	a.Atime = uint64(s.Atim.Sec)
	a.Atimensec = uint32(s.Atim.Nsec)
	a.Mtime = uint64(s.Mtim.Sec)
	a.Mtimensec = uint32(s.Mtim.Nsec)
	a.Ctime = uint64(s.Ctim.Sec)
	a.Ctimensec = uint32(s.Ctim.Nsec)
	a.Mode = s.Mode
	a.Nlink = uint32(s.Nlink)
	a.Uid = uint32(s.Uid)
	a.Gid = uint32(s.Gid)
	a.Rdev = uint32(s.Rdev)
	a.Blksize = uint32(s.Blksize)
}

func (a *Statx) FromStatx(s *unix.Statx_t) {
	a.Ino = uint64(s.Ino)
	a.Size = uint64(s.Size)
	a.Blocks = uint64(s.Blocks)
	a.Atime.FromStatxTimestamp(&s.Atime)
	a.Btime.FromStatxTimestamp(&s.Btime)
	a.Ctime.FromStatxTimestamp(&s.Ctime)
	a.Mtime.FromStatxTimestamp(&s.Mtime)
	a.Mode = s.Mode
	a.Nlink = uint32(s.Nlink)
	a.Uid = uint32(s.Uid)
	a.Gid = uint32(s.Gid)
	a.Blksize = uint32(s.Blksize)
	a.AttributesMask = s.Attributes_mask
	a.Mask = s.Mask
	a.Attributes = s.Attributes
	a.RdevMinor = s.Rdev_minor
	a.RdevMajor = s.Rdev_major
	a.DevMajor = s.Dev_major
	a.DevMinor = s.Dev_minor
}
