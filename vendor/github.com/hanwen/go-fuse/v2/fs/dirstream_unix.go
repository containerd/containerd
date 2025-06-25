//go:build !darwin

// Copyright 2019 the Go-FUSE Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fs

import "golang.org/x/sys/unix"

func getdents(fd int, buf []byte) (int, error) {
	return unix.Getdents(fd, buf)
}
