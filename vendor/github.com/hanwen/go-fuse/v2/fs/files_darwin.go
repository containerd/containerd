// Copyright 2019 the Go-FUSE Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fs

import (
	"context"
	"syscall"
	"time"

	"github.com/hanwen/go-fuse/v2/fuse"
	"github.com/hanwen/go-fuse/v2/internal/utimens"
)

func setBlocks(out *fuse.Attr) {
}

// MacOS before High Sierra lacks utimensat() and UTIME_OMIT.
// We emulate using utimes() and extra Getattr() calls.
func (f *loopbackFile) utimens(a *time.Time, m *time.Time) syscall.Errno {
	var attr fuse.AttrOut
	if a == nil || m == nil {
		errno := f.Getattr(context.Background(), &attr)
		if errno != 0 {
			return errno
		}
	}
	tv := utimens.Fill(a, m, &attr.Attr)
	err := syscall.Futimes(int(f.fd), tv)
	return ToErrno(err)
}
