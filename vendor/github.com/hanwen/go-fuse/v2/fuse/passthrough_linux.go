// Copyright 2024 the Go-FUSE Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fuse

import (
	"syscall"
	"unsafe"
)

const (
	_DEV_IOC_BACKING_OPEN  = 0x4010e501
	_DEV_IOC_BACKING_CLOSE = 0x4004e502
)

// RegisterBackingFd registers the given file descriptor in the
// kernel, so the kernel can bypass FUSE and access the backing file
// directly for read and write calls. On success a backing ID is
// returned. The backing ID should unregistered using
// UnregisterBackingFd() once the file is released.  Within the
// kernel, an inode can only have a single backing file, so multiple
// Open/Create calls should coordinate to return a consistent backing
// ID.
func (ms *Server) RegisterBackingFd(m *BackingMap) (int32, syscall.Errno) {
	ms.writeMu.Lock()
	id, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(ms.mountFd), uintptr(_DEV_IOC_BACKING_OPEN), uintptr(unsafe.Pointer(m)))
	ms.writeMu.Unlock()
	if ms.opts.Debug {
		ms.opts.Logger.Printf("ioctl: BACKING_OPEN %v: id %d (%v)", m.string(), id, errno)
	}
	return int32(id), errno
}

// UnregisterBackingFd unregisters the given ID in the kernel. The ID
// should have been acquired before using RegisterBackingFd.
func (ms *Server) UnregisterBackingFd(id int32) syscall.Errno {
	ms.writeMu.Lock()
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(ms.mountFd), uintptr(_DEV_IOC_BACKING_CLOSE), uintptr(unsafe.Pointer(&id)))
	ms.writeMu.Unlock()

	if ms.opts.Debug {
		ms.opts.Logger.Printf("ioctl: BACKING_CLOSE id %d: %v", id, errno)
	}
	return errno
}
