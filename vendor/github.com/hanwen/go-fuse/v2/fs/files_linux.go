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

func setBlocks(out *fuse.Attr) {
	if out.Blksize > 0 {
		return
	}

	out.Blksize = 4096
	pages := (out.Size + 4095) / 4096
	out.Blocks = pages * 8
}

func setStatxBlocks(out *fuse.Statx) {
	if out.Blksize > 0 {
		return
	}

	out.Blksize = 4096
	pages := (out.Size + 4095) / 4096
	out.Blocks = pages * 8
}

func (f *loopbackFile) Statx(ctx context.Context, flags uint32, mask uint32, out *fuse.StatxOut) syscall.Errno {
	f.mu.Lock()
	defer f.mu.Unlock()
	st := unix.Statx_t{}
	err := unix.Statx(f.fd, "", int(flags), int(mask), &st)
	if err != nil {
		return ToErrno(err)
	}
	out.FromStatx(&st)

	return OK
}
