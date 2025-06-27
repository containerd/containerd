// Copyright 2024 the Go-FUSE Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fs

import (
	"context"
	"syscall"

	"github.com/hanwen/go-fuse/v2/internal/xattr"
	"golang.org/x/sys/unix"
)

const unix_UTIME_OMIT = unix.UTIME_OMIT

// FreeBSD has added copy_file_range(2) since FreeBSD 12. However,
// golang.org/x/sys/unix hasn't add corresponding syscall constant or
// wrap function. Here we define the syscall constant until sys/unix
// provides.
const sys_COPY_FILE_RANGE = 569

// TODO: replace the manual syscall when sys/unix provides CopyFileRange
// for FreeBSD
func doCopyFileRange(fdIn int, offIn int64, fdOut int, offOut int64,
	len int, flags int) (uint32, syscall.Errno) {
	count, _, errno := unix.Syscall6(sys_COPY_FILE_RANGE,
		uintptr(fdIn), uintptr(offIn), uintptr(fdOut), uintptr(offOut),
		uintptr(len), uintptr(flags),
	)
	return uint32(count), errno
}

func intDev(dev uint32) uint64 {
	return uint64(dev)
}

// Since FUSE on FreeBSD expect Linux flavor data format of
// listxattr, we should reconstruct it with data returned by
// FreeBSD's syscall. And here we have added a "user." prefix
// to put them under "user" namespace, which is readable and
// writable for normal user, for a userspace implemented FS.
func rebuildAttrBuf(attrList [][]byte) []byte {
	ret := make([]byte, 0)
	for _, attrName := range attrList {
		nsAttrName := append([]byte("user."), attrName...)
		ret = append(ret, nsAttrName...)
		ret = append(ret, 0x0)
	}
	return ret
}

var _ = (NodeListxattrer)((*LoopbackNode)(nil))

func (n *LoopbackNode) Listxattr(ctx context.Context, dest []byte) (uint32, syscall.Errno) {
	// In order to simulate same data format as Linux does,
	// and the size of returned buf is required to match, we must
	// call unix.Llistxattr twice.
	sz, err := unix.Llistxattr(n.path(), nil)
	if err != nil {
		return uint32(sz), ToErrno(err)
	}
	rawBuf := make([]byte, sz)
	sz, err = unix.Llistxattr(n.path(), rawBuf)
	if err != nil {
		return uint32(sz), ToErrno(err)
	}
	attrList := xattr.ParseAttrNames(rawBuf)
	rebuiltBuf := rebuildAttrBuf(attrList)
	sz = len(rebuiltBuf)
	if len(dest) != 0 {
		// When len(dest) is 0, which means that caller wants to get
		// the size. If len(dest) is less than len(rebuiltBuf), but greater
		// than 0 dest will be also filled with data from rebuiltBuf,
		// but truncated to len(dest). copy() function will do the same.
		// And this behaviour is same as FreeBSD's syscall extattr_list_file(2).
		sz = copy(dest, rebuiltBuf)
	}
	return uint32(sz), ToErrno(err)
}
