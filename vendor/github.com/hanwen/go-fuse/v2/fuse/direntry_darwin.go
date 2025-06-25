// Copyright 2024 the Go-FUSE Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fuse

// Like syscall.Dirent, but without the [256]byte name.
type dirent struct {
	Ino    uint64
	Off    int64
	Reclen uint16
	Namlen uint16
	Type   uint8
	Name   [1]uint8 // align to 4 bytes for 32 bits.
}

func (de *dirent) nameLength() int {
	return int(de.Namlen)
}
