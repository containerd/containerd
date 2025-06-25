// Copyright 2016 the Go-FUSE Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fuse

import (
	"syscall"

	"golang.org/x/sys/unix"
)

const (
	ENODATA = Status(syscall.ENODATA)
	ENOATTR = Status(syscall.ENODATA) // On Linux, ENOATTR is an alias for ENODATA.

	// EREMOTEIO Remote I/O error
	EREMOTEIO = Status(syscall.EREMOTEIO)
)

// To be set in InitIn/InitOut.Flags.
//
// This flags conflict with https://github.com/macfuse/library/blob/master/include/fuse_common.h
// and should be used only on Linux.
const (
	CAP_NO_OPENDIR_SUPPORT  = (1 << 24)
	CAP_EXPLICIT_INVAL_DATA = (1 << 25)

	CAP_MAP_ALIGNMENT      = (1 << 26)
	CAP_SUBMOUNTS          = (1 << 27)
	CAP_HANDLE_KILLPRIV_V2 = (1 << 28)
	CAP_SETXATTR_EXT       = (1 << 29)
	CAP_INIT_EXT           = (1 << 30)
	CAP_INIT_RESERVED      = (1 << 31)

	// CAP_RENAME_SWAP only exists on OSX.
	CAP_RENAME_SWAP = 0x0
)

func (s *StatfsOut) FromStatfsT(statfs *syscall.Statfs_t) {
	s.Blocks = statfs.Blocks
	s.Bsize = uint32(statfs.Bsize)
	s.Bfree = statfs.Bfree
	s.Bavail = statfs.Bavail
	s.Files = statfs.Files
	s.Ffree = statfs.Ffree
	s.Frsize = uint32(statfs.Frsize)
	s.NameLen = uint32(statfs.Namelen)
}

func (o *InitOut) setFlags(flags uint64) {
	o.Flags = uint32(flags) | CAP_INIT_EXT
	o.Flags2 = uint32(flags >> 32)
}

func (t *SxTime) FromStatxTimestamp(ts *unix.StatxTimestamp) {
	t.Sec = uint64(ts.Sec)
	t.Nsec = ts.Nsec
}
