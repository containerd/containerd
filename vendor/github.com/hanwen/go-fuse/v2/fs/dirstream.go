// Copyright 2019 the Go-FUSE Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fs

import (
	"context"
	"sync"
	"syscall"

	"github.com/hanwen/go-fuse/v2/fuse"
	"golang.org/x/sys/unix"
)

type dirArray struct {
	idx     int
	entries []fuse.DirEntry
}

func (a *dirArray) HasNext() bool {
	return a.idx < len(a.entries)
}

func (a *dirArray) Next() (fuse.DirEntry, syscall.Errno) {
	e := a.entries[a.idx]
	a.idx++
	e.Off = uint64(a.idx)
	return e, 0
}

func (a *dirArray) Seekdir(ctx context.Context, off uint64) syscall.Errno {
	idx := int(off)
	if idx < 0 || idx > len(a.entries) {
		return syscall.EINVAL
	}
	a.idx = idx
	return 0
}

func (a *dirArray) Close() {

}

func (a *dirArray) Releasedir(ctx context.Context, releaseFlags uint32) {}

func (a *dirArray) Readdirent(ctx context.Context) (de *fuse.DirEntry, errno syscall.Errno) {
	if !a.HasNext() {
		return nil, 0
	}
	e, errno := a.Next()
	return &e, errno
}

// NewLoopbackDirStream opens a directory for reading as a DirStream
func NewLoopbackDirStream(name string) (DirStream, syscall.Errno) {
	// TODO: should return concrete type.
	fd, err := syscall.Open(name, syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0755)
	if err != nil {
		return nil, ToErrno(err)
	}
	return NewLoopbackDirStreamFd(fd)
}

// NewListDirStream wraps a slice of DirEntry as a DirStream.
func NewListDirStream(list []fuse.DirEntry) DirStream {
	return &dirArray{entries: list}
}

// implement FileReaddirenter/FileReleasedirer
type dirStreamAsFile struct {
	creator func(context.Context) (DirStream, syscall.Errno)
	ds      DirStream
}

func (d *dirStreamAsFile) Releasedir(ctx context.Context, releaseFlags uint32) {
	if d.ds != nil {
		d.ds.Close()
	}
}

func (d *dirStreamAsFile) Readdirent(ctx context.Context) (de *fuse.DirEntry, errno syscall.Errno) {
	if d.ds == nil {
		d.ds, errno = d.creator(ctx)
		if errno != 0 {
			return nil, errno
		}
	}
	if !d.ds.HasNext() {
		return nil, 0
	}

	e, errno := d.ds.Next()
	return &e, errno
}

func (d *dirStreamAsFile) Seekdir(ctx context.Context, off uint64) syscall.Errno {
	if d.ds == nil {
		var errno syscall.Errno
		d.ds, errno = d.creator(ctx)
		if errno != 0 {
			return errno
		}
	}
	if sd, ok := d.ds.(FileSeekdirer); ok {
		return sd.Seekdir(ctx, off)
	}
	return syscall.ENOTSUP
}

type loopbackDirStream struct {
	buf []byte

	// Protects mutable members
	mu sync.Mutex

	// mutable
	todo      []byte
	todoErrno syscall.Errno
	fd        int
}

// NewLoopbackDirStreamFd reads the directory opened at file descriptor fd as
// a DirStream
func NewLoopbackDirStreamFd(fd int) (DirStream, syscall.Errno) {
	ds := &loopbackDirStream{
		buf: make([]byte, 4096),
		fd:  fd,
	}
	ds.load()
	return ds, OK
}

func (ds *loopbackDirStream) Close() {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	if ds.fd != -1 {
		syscall.Close(ds.fd)
		ds.fd = -1
	}
}

var _ = (FileReleasedirer)((*loopbackDirStream)(nil))

func (ds *loopbackDirStream) Releasedir(ctx context.Context, flags uint32) {
	ds.Close()
}

var _ = (FileSeekdirer)((*loopbackDirStream)(nil))

func (ds *loopbackDirStream) Seekdir(ctx context.Context, off uint64) syscall.Errno {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	_, errno := unix.Seek(ds.fd, int64(off), unix.SEEK_SET)
	if errno != nil {
		return ToErrno(errno)
	}

	ds.todo = nil
	ds.todoErrno = 0
	ds.load()
	return 0
}

var _ = (FileFsyncdirer)((*loopbackDirStream)(nil))

func (ds *loopbackDirStream) Fsyncdir(ctx context.Context, flags uint32) syscall.Errno {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	return ToErrno(syscall.Fsync(ds.fd))
}

func (ds *loopbackDirStream) HasNext() bool {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	return len(ds.todo) > 0 || ds.todoErrno != 0
}

var _ = (FileReaddirenter)((*loopbackDirStream)(nil))

func (ds *loopbackDirStream) Readdirent(ctx context.Context) (*fuse.DirEntry, syscall.Errno) {
	if !ds.HasNext() {
		return nil, 0
	}
	de, errno := ds.Next()
	return &de, errno
}

func (ds *loopbackDirStream) Next() (fuse.DirEntry, syscall.Errno) {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	if ds.todoErrno != 0 {
		return fuse.DirEntry{}, ds.todoErrno
	}
	var res fuse.DirEntry
	n := res.Parse(ds.todo)
	ds.todo = ds.todo[n:]
	if len(ds.todo) == 0 {
		ds.load()
	}
	return res, 0
}

func (ds *loopbackDirStream) load() {
	if len(ds.todo) > 0 {
		return
	}

	n, err := getdents(ds.fd, ds.buf)
	if n < 0 {
		n = 0
	}
	ds.todo = ds.buf[:n]
	ds.todoErrno = ToErrno(err)
}
