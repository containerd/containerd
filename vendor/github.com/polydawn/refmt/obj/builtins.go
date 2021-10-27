package obj

import (
	. "reflect"
)

var (
	rtid_bool    = ValueOf(TypeOf(false)).Pointer()
	rtid_string  = ValueOf(TypeOf("")).Pointer()
	rtid_bytes   = ValueOf(TypeOf([]byte{})).Pointer()
	rtid_int     = ValueOf(TypeOf(int(0))).Pointer()
	rtid_int8    = ValueOf(TypeOf(int8(0))).Pointer()
	rtid_int16   = ValueOf(TypeOf(int16(0))).Pointer()
	rtid_int32   = ValueOf(TypeOf(int32(0))).Pointer()
	rtid_int64   = ValueOf(TypeOf(int64(0))).Pointer()
	rtid_uint    = ValueOf(TypeOf(uint(0))).Pointer()
	rtid_uint8   = ValueOf(TypeOf(uint8(0))).Pointer()
	rtid_uint16  = ValueOf(TypeOf(uint16(0))).Pointer()
	rtid_uint32  = ValueOf(TypeOf(uint32(0))).Pointer()
	rtid_uint64  = ValueOf(TypeOf(uint64(0))).Pointer()
	rtid_uintptr = ValueOf(TypeOf(uintptr(0))).Pointer()
	rtid_float32 = ValueOf(TypeOf(float32(0))).Pointer()
	rtid_float64 = ValueOf(TypeOf(float64(0))).Pointer()
)
