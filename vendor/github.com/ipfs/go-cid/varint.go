package cid

import (
	"github.com/multiformats/go-varint"
)

// Version of varint function that works with a string rather than
// []byte to avoid unnecessary allocation

// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license as given at https://golang.org/LICENSE

// uvarint decodes a uint64 from buf and returns that value and the
// number of bytes read (> 0). If an error occurred, then 0 is
// returned for both the value and the number of bytes read, and an
// error is returned.
func uvarint(buf string) (uint64, int, error) {
	var x uint64
	var s uint
	// we have a binary string so we can't use a range loop
	for i := 0; i < len(buf); i++ {
		b := buf[i]
		if b < 0x80 {
			if i > 9 || i == 9 && b > 1 {
				return 0, 0, varint.ErrOverflow
			}
			if b == 0 && i > 0 {
				return 0, 0, varint.ErrNotMinimal
			}
			return x | uint64(b)<<s, i + 1, nil
		}
		x |= uint64(b&0x7f) << s
		s += 7
	}
	return 0, 0, varint.ErrUnderflow
}
