// Copyright 2024 the Go-FUSE Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xattr

// BSDs syscall use different convention of data buf retrieved
// through syscall `unix.Listxattr`.
// Ref: extattr_list_file(2)
func parseAttrNames(buf []byte) [][]byte {
	var attrList [][]byte
	for p := 0; p < len(buf); {
		attrNameLen := int(buf[p])
		p++
		attrName := buf[p : p+attrNameLen]
		attrList = append(attrList, attrName)
		p += attrNameLen
	}
	return attrList
}
