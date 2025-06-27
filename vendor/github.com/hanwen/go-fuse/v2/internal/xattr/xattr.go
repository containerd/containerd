// Copyright 2024 the Go-FUSE Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xattr

func ParseAttrNames(buf []byte) [][]byte {
	return parseAttrNames(buf)
}
