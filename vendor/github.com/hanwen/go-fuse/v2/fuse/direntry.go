// Copyright 2016 the Go-FUSE Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fuse

import (
	"bytes"
	"fmt"
	"unsafe"
)

const direntSize = int(unsafe.Sizeof(_Dirent{}))

// DirEntry is a type for PathFileSystem and NodeFileSystem to return
// directory contents in.
type DirEntry struct {
	// Mode is the file's mode. Only the high bits (eg. S_IFDIR)
	// are considered.
	Mode uint32

	// Name is the basename of the file in the directory.
	Name string

	// Ino is the inode number.
	Ino uint64

	// Off is the offset in the directory stream. The offset is
	// thought to be after the entry.
	Off uint64
}

func (d *DirEntry) String() string {
	return fmt.Sprintf("%d: %q ino=%d (%o)", d.Off, d.Name, d.Ino, d.Mode)
}

// Parse reads an entry from getdents(2) buffer. It returns the number
// of bytes consumed.
func (d *DirEntry) Parse(buf []byte) int {
	// We can't use syscall.Dirent here, because it declares a
	// [256]byte name, which may run beyond the end of ds.todo.
	// when that happens in the race detector, it causes a panic
	// "converted pointer straddles multiple allocations"
	de := (*dirent)(unsafe.Pointer(&buf[0]))
	off := unsafe.Offsetof(dirent{}.Name)
	nameBytes := buf[off : off+uintptr(de.nameLength())]
	n := de.Reclen

	l := bytes.IndexByte(nameBytes, 0)
	if l >= 0 {
		nameBytes = nameBytes[:l]
	}
	*d = DirEntry{
		Ino:  de.Ino,
		Mode: (uint32(de.Type) << 12),
		Name: string(nameBytes),
		Off:  uint64(de.Off),
	}
	return int(n)
}

// DirEntryList holds the return value for READDIR and READDIRPLUS
// opcodes.
type DirEntryList struct {
	buf []byte
	// capacity of the underlying buffer
	size int

	// Offset holds the offset for the next entry to be added. It
	// is the offset supplied at construction time, or the Offset
	// of the last DirEntry that was added.
	Offset uint64

	// pointer to the last serialized _Dirent. Used by FixMode().
	lastDirent *_Dirent
}

// NewDirEntryList creates a DirEntryList with the given data buffer
// and offset.
func NewDirEntryList(data []byte, off uint64) *DirEntryList {
	return &DirEntryList{
		buf:    data[:0],
		size:   len(data),
		Offset: off,
	}
}

// AddDirEntry tries to add an entry, and reports whether it
// succeeded.  If adding a 0 offset entry, the offset is taken to be
// the last offset + 1.
func (l *DirEntryList) AddDirEntry(e DirEntry) bool {
	// TODO: take pointer arg, merge with AddDirLookupEntry.
	return l.addDirEntry(&e, 0)
}

func (l *DirEntryList) addDirEntry(e *DirEntry, prefix int) bool {
	if e.Ino == 0 {
		e.Ino = FUSE_UNKNOWN_INO
	}
	if e.Off == 0 {
		e.Off = l.Offset + 1
	}
	padding := (8 - len(e.Name)&7) & 7
	delta := padding + direntSize + len(e.Name) + prefix
	oldLen := len(l.buf)
	newLen := delta + oldLen

	if newLen > l.size {
		return false
	}
	l.buf = l.buf[:newLen]
	oldLen += prefix
	dirent := (*_Dirent)(unsafe.Pointer(&l.buf[oldLen]))
	dirent.Off = e.Off
	dirent.Ino = e.Ino
	dirent.NameLen = uint32(len(e.Name))
	dirent.Typ = modeToType(e.Mode)
	oldLen += direntSize
	copy(l.buf[oldLen:], e.Name)
	oldLen += len(e.Name)

	if padding > 0 {
		l.buf[oldLen] = 0
	}
	l.Offset = dirent.Off
	return true
}

// Add adds a direntry to the DirEntryList, returning wheither it
// succeeded. Prefix is the amount of padding to add before the DirEntry.
//
// Deprecated: use AddDirLookupEntry or AddDirEntry.
func (l *DirEntryList) Add(prefix int, name string, inode uint64, mode uint32) bool {
	// TODO: remove.
	e := DirEntry{
		Name: name,
		Mode: mode,
		Off:  l.Offset + 1,
		Ino:  inode,
	}
	return l.addDirEntry(&e, prefix)
}

// AddDirLookupEntry is used for ReadDirPlus. If reserves and zeroes
// space for an EntryOut struct and serializes the DirEntry. If adding
// a 0 offset entry, the offset is taken to be the last offset + 1.
// If the entry does not fit, it returns nil.
func (l *DirEntryList) AddDirLookupEntry(e DirEntry) *EntryOut {
	// The resulting READDIRPLUS output buffer looks like this in memory:
	// 1) EntryOut{}
	// 2) _Dirent{}
	// 3) Name (null-terminated)
	// 4) Padding to align to 8 bytes
	// [repeat]

	// TODO: should take pointer as argument.
	const entryOutSize = int(unsafe.Sizeof(EntryOut{}))
	oldLen := len(l.buf)
	ok := l.addDirEntry(&e, entryOutSize)
	if !ok {
		return nil
	}
	l.lastDirent = (*_Dirent)(unsafe.Pointer(&l.buf[oldLen+entryOutSize]))
	entryOut := (*EntryOut)(unsafe.Pointer(&l.buf[oldLen]))
	*entryOut = EntryOut{}
	return entryOut
}

// modeToType converts a file *mode* (as used in syscall.Stat_t.Mode)
// to a file *type* (as used in _Dirent.Typ).
// Equivalent to IFTODT() in libc (see man 5 dirent).
func modeToType(mode uint32) uint32 {
	return (mode & 0170000) >> 12
}

// FixMode overrides the file mode of the last direntry that was added. This can
// be needed when a directory changes while READDIRPLUS is running.
// Only the file type bits of mode are considered, the rest is masked out.
func (l *DirEntryList) FixMode(mode uint32) {
	l.lastDirent.Typ = modeToType(mode)
}

func (l *DirEntryList) bytes() []byte {
	return l.buf
}
