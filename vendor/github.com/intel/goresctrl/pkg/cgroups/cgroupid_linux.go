//go:build linux
// +build linux

package cgroups

import (
	"encoding/binary"
	"golang.org/x/sys/unix"
)

func getID(path string) uint64 {
	h, _, err := unix.NameToHandleAt(unix.AT_FDCWD, path, 0)
	if err != nil {
		return 0
	}

	return binary.LittleEndian.Uint64(h.Bytes())
}
