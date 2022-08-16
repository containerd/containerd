//go:build go1.17
// +build go1.17

package creflect

import (
	"unsafe"
)

// name is an encoded type name with optional extra data.
type name struct {
	bytes *byte
}

func (n name) data(off int, whySafe string) *byte {
	return (*byte)(add(unsafe.Pointer(n.bytes), uintptr(off), whySafe))
}

func (n name) readVarint(off int) (int, int) {
	v := 0
	for i := 0; ; i++ {
		x := *n.data(off+i, "read varint")
		v += int(x&0x7f) << (7 * i)
		if x&0x80 == 0 {
			return i + 1, v
		}
	}
}

func (n name) name() (s string) {
	if n.bytes == nil {
		return
	}
	i, l := n.readVarint(1)
	hdr := (*String)(unsafe.Pointer(&s))
	hdr.Data = unsafe.Pointer(n.data(1+i, "non-empty string"))
	hdr.Len = l
	return
}
