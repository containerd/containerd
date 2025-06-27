//go:build !freebsd

// Copyright 2024 the Go-FUSE Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
package fs

import (
	"context"
	"syscall"

	"golang.org/x/sys/unix"
)

var _ = (NodeListxattrer)((*LoopbackNode)(nil))

func (n *LoopbackNode) Listxattr(ctx context.Context, dest []byte) (uint32, syscall.Errno) {
	sz, err := unix.Llistxattr(n.path(), dest)
	return uint32(sz), ToErrno(err)
}
