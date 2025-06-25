// Copyright 2024 the Go-FUSE Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fuse

type dirent struct {
	Ino    uint64
	Off    int64
	Reclen uint16
	Type   uint8
	Pad0   uint8
	Namlen uint16
	Pad1   uint16
	Name   [1]int8
}

func (de *dirent) nameLength() int {
	return int(de.Namlen)
}
