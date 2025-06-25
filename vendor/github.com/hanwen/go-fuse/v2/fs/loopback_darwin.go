//go:build darwin
// +build darwin

// Copyright 2019 the Go-FUSE Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fs

import (
	"syscall"
	"time"
)

const unix_UTIME_OMIT = 0x0

// timeToTimeval - Convert time.Time to syscall.Timeval
//
// Note: This does not use syscall.NsecToTimespec because
// that does not work properly for times before 1970,
// see https://github.com/golang/go/issues/12777
func timeToTimeval(t *time.Time) syscall.Timeval {
	var tv syscall.Timeval
	tv.Usec = int32(t.Nanosecond() / 1000)
	tv.Sec = t.Unix()
	return tv
}

func doCopyFileRange(fdIn int, offIn int64, fdOut int, offOut int64,
	len int, flags int) (uint32, syscall.Errno) {
	return 0, syscall.ENOSYS
}

func intDev(dev uint32) int {
	return int(dev)
}
