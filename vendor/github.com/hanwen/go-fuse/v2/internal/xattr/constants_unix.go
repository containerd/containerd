//go:build !linux

// Copyright 2024 the Go-FUSE Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xattr

import "golang.org/x/sys/unix"

const ENOATTR = unix.ENOATTR
