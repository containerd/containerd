//go:build !linux

// Copyright 2025 the Go-FUSE Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fs

func keepSizeMode(mode uint32) bool {
	return false
}
