//go:build !freebsd

// Copyright 2024 the Go-FUSE Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xattr

import "bytes"

func parseAttrNames(buf []byte) [][]byte {
	return bytes.Split(buf, []byte{0})
}
