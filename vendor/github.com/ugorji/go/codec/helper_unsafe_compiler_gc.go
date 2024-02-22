// Copyright (c) 2012-2020 Ugorji Nwoke. All rights reserved.
// Use of this source code is governed by a MIT license found in the LICENSE file.

//go:build !safe && !codec.safe && !appengine && go1.9 && gc
// +build !safe,!codec.safe,!appengine,go1.9,gc

package codec

import (
	"reflect"
	_ "runtime" // needed for go linkname(s)
	"unsafe"
)

// keep in sync with
//
//	$GOROOT/src/cmd/compile/internal/gc/reflect.go: MAXKEYSIZE, MAXELEMSIZE
//	$GOROOT/src/runtime/map.go: maxKeySize, maxElemSize
//	$GOROOT/src/reflect/type.go: maxKeySize, maxElemSize
//
// We use these to determine whether the type is stored indirectly in the map or not.
const (
	// mapMaxKeySize  = 128
	mapMaxElemSize = 128
)

func unsafeGrowslice(typ unsafe.Pointer, old unsafeSlice, cap, incr int) (v unsafeSlice) {
	return growslice(typ, old, cap+incr)
}

// func rvType(rv reflect.Value) reflect.Type {
// 	return rvPtrToType(((*unsafeReflectValue)(unsafe.Pointer(&rv))).typ)
// 	// return rv.Type()
// }

// mapStoresElemIndirect tells if the element type is stored indirectly in the map.
//
// This is used to determine valIsIndirect which is passed into mapSet/mapGet calls.
//
// If valIsIndirect doesn't matter, then just return false and ignore the value
// passed in mapGet/mapSet calls
func mapStoresElemIndirect(elemsize uintptr) bool {
	return elemsize > mapMaxElemSize
}

func mapSet(m, k, v reflect.Value, keyFastKind mapKeyFastKind, valIsIndirect, valIsRef bool) {
	var urv = (*unsafeReflectValue)(unsafe.Pointer(&k))
	var kptr = unsafeMapKVPtr(urv)
	urv = (*unsafeReflectValue)(unsafe.Pointer(&v))
	var vtyp = urv.typ
	var vptr = unsafeMapKVPtr(urv)

	urv = (*unsafeReflectValue)(unsafe.Pointer(&m))
	mptr := rvRefPtr(urv)

	var vvptr unsafe.Pointer

	// mapassign_fastXXX don't take indirect into account.
	// It was hard to infer what makes it work all the time.
	// Sometimes, we got vvptr == nil when we dereferenced vvptr (if valIsIndirect).
	// Consequently, only use fastXXX functions if !valIsIndirect

	if valIsIndirect {
		vvptr = mapassign(urv.typ, mptr, kptr)
		typedmemmove(vtyp, vvptr, vptr)
		// reflect_mapassign(urv.typ, mptr, kptr, vptr)
		return
	}

	switch keyFastKind {
	case mapKeyFastKind32:
		vvptr = mapassign_fast32(urv.typ, mptr, *(*uint32)(kptr))
	case mapKeyFastKind32ptr:
		vvptr = mapassign_fast32ptr(urv.typ, mptr, *(*unsafe.Pointer)(kptr))
	case mapKeyFastKind64:
		vvptr = mapassign_fast64(urv.typ, mptr, *(*uint64)(kptr))
	case mapKeyFastKind64ptr:
		vvptr = mapassign_fast64ptr(urv.typ, mptr, *(*unsafe.Pointer)(kptr))
	case mapKeyFastKindStr:
		vvptr = mapassign_faststr(urv.typ, mptr, *(*string)(kptr))
	default:
		vvptr = mapassign(urv.typ, mptr, kptr)
	}

	// if keyFastKind != 0 && valIsIndirect {
	// 	vvptr = *(*unsafe.Pointer)(vvptr)
	// }

	typedmemmove(vtyp, vvptr, vptr)
}

func mapGet(m, k, v reflect.Value, keyFastKind mapKeyFastKind, valIsIndirect, valIsRef bool) (_ reflect.Value) {
	var urv = (*unsafeReflectValue)(unsafe.Pointer(&k))
	var kptr = unsafeMapKVPtr(urv)
	urv = (*unsafeReflectValue)(unsafe.Pointer(&m))
	mptr := rvRefPtr(urv)

	var vvptr unsafe.Pointer
	var ok bool

	// Note that mapaccess2_fastXXX functions do not check if the value needs to be copied.
	// if they do, we should dereference the pointer and return that

	switch keyFastKind {
	case mapKeyFastKind32, mapKeyFastKind32ptr:
		vvptr, ok = mapaccess2_fast32(urv.typ, mptr, *(*uint32)(kptr))
	case mapKeyFastKind64, mapKeyFastKind64ptr:
		vvptr, ok = mapaccess2_fast64(urv.typ, mptr, *(*uint64)(kptr))
	case mapKeyFastKindStr:
		vvptr, ok = mapaccess2_faststr(urv.typ, mptr, *(*string)(kptr))
	default:
		vvptr, ok = mapaccess2(urv.typ, mptr, kptr)
	}

	if !ok {
		return
	}

	urv = (*unsafeReflectValue)(unsafe.Pointer(&v))

	if keyFastKind != 0 && valIsIndirect {
		urv.ptr = *(*unsafe.Pointer)(vvptr)
	} else if helperUnsafeDirectAssignMapEntry || valIsRef {
		urv.ptr = vvptr
	} else {
		typedmemmove(urv.typ, urv.ptr, vvptr)
	}

	return v
}

//go:linkname unsafeZeroArr runtime.zeroVal
var unsafeZeroArr [1024]byte

// //go:linkname rvPtrToType reflect.toType
// //go:noescape
// func rvPtrToType(typ unsafe.Pointer) reflect.Type

//go:linkname mapassign_fast32 runtime.mapassign_fast32
//go:noescape
func mapassign_fast32(typ unsafe.Pointer, m unsafe.Pointer, key uint32) unsafe.Pointer

//go:linkname mapassign_fast32ptr runtime.mapassign_fast32ptr
//go:noescape
func mapassign_fast32ptr(typ unsafe.Pointer, m unsafe.Pointer, key unsafe.Pointer) unsafe.Pointer

//go:linkname mapassign_fast64 runtime.mapassign_fast64
//go:noescape
func mapassign_fast64(typ unsafe.Pointer, m unsafe.Pointer, key uint64) unsafe.Pointer

//go:linkname mapassign_fast64ptr runtime.mapassign_fast64ptr
//go:noescape
func mapassign_fast64ptr(typ unsafe.Pointer, m unsafe.Pointer, key unsafe.Pointer) unsafe.Pointer

//go:linkname mapassign_faststr runtime.mapassign_faststr
//go:noescape
func mapassign_faststr(typ unsafe.Pointer, m unsafe.Pointer, s string) unsafe.Pointer

//go:linkname mapaccess2_fast32 runtime.mapaccess2_fast32
//go:noescape
func mapaccess2_fast32(typ unsafe.Pointer, m unsafe.Pointer, key uint32) (val unsafe.Pointer, ok bool)

//go:linkname mapaccess2_fast64 runtime.mapaccess2_fast64
//go:noescape
func mapaccess2_fast64(typ unsafe.Pointer, m unsafe.Pointer, key uint64) (val unsafe.Pointer, ok bool)

//go:linkname mapaccess2_faststr runtime.mapaccess2_faststr
//go:noescape
func mapaccess2_faststr(typ unsafe.Pointer, m unsafe.Pointer, key string) (val unsafe.Pointer, ok bool)
