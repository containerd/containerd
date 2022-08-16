//go:build !go1.17
// +build !go1.17

package creflect

import (
	"unsafe"
)

// name is an encoded type name with optional extra data.
type name struct {
	bytes *byte
}

func (n name) name() (s string) {
	if n.bytes == nil {
		return
	}
	b := (*[4]byte)(unsafe.Pointer(n.bytes))

	hdr := (*String)(unsafe.Pointer(&s))
	hdr.Data = unsafe.Pointer(&b[3])
	hdr.Len = int(b[1])<<8 | int(b[2])
	return s
}
