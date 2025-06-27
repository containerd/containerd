//go:build !darwin

package fuse

import "golang.org/x/sys/unix"

const _UTIME_OMIT = unix.UTIME_OMIT
