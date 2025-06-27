//go:build linux
// +build linux

// Copyright 2019 the Go-FUSE Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fs

import (
	"context"
	"syscall"

	"github.com/hanwen/go-fuse/v2/fuse"
	"golang.org/x/sys/unix"
)

const unix_UTIME_OMIT = unix.UTIME_OMIT

func doCopyFileRange(fdIn int, offIn int64, fdOut int, offOut int64,
	len int, flags int) (uint32, syscall.Errno) {
	count, err := unix.CopyFileRange(fdIn, &offIn, fdOut, &offOut, len, flags)
	return uint32(count), ToErrno(err)
}

func intDev(dev uint32) int {
	return int(dev)
}

var _ = (NodeStatxer)((*LoopbackNode)(nil))

func (n *LoopbackNode) Statx(ctx context.Context, f FileHandle,
	flags uint32, mask uint32,
	out *fuse.StatxOut) syscall.Errno {
	if f != nil {
		if fga, ok := f.(FileStatxer); ok {
			return fga.Statx(ctx, flags, mask, out)
		}
	}

	p := n.path()

	st := unix.Statx_t{}
	err := unix.Statx(unix.AT_FDCWD, p, int(flags), int(mask), &st)
	if err != nil {
		return ToErrno(err)
	}
	out.FromStatx(&st)
	return OK
}
