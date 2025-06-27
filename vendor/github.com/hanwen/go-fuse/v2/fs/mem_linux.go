// Copyright 2025 the Go-FUSE Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fs

import "golang.org/x/sys/unix"

func keepSizeMode(mode uint32) bool {
	return mode&unix.FALLOC_FL_KEEP_SIZE != 0
}
