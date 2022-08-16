// Customized reflect package for gomonkey，copy most code from go/src/reflect/type.go

package creflect

import (
	"reflect"
	"unsafe"
)

// rtype is the common implementation of most values.
// rtype must be kept in sync with ../runtime/type.go:/^type._type.
type rtype struct {
	size       uintptr
	ptrdata    uintptr // number of bytes in the type that can contain pointers
	hash       uint32  // hash of type; avoids computation in hash tables
	tflag      tflag   // extra type information flags
	align      uint8   // alignment of variable with this type
	fieldAlign uint8   // alignment of struct field with this type
	kind       uint8   // enumeration for C
	// function for comparing objects of this type
	// (ptr to object A, ptr to object B) -> ==?
	equal     func(unsafe.Pointer, unsafe.Pointer) bool
	gcdata    *byte   // garbage collection data
	str       nameOff // string form
	ptrToThis typeOff // type for pointer to this type, may be zero
}

func Create(t reflect.Type) *rtype {
	i := *(*funcValue)(unsafe.Pointer(&t))
	r := (*rtype)(i.p)
	return r
}

type funcValue struct {
	_ uintptr
	p unsafe.Pointer
}

func funcPointer(v reflect.Method, ok bool) (unsafe.Pointer, bool) {
	return (*funcValue)(unsafe.Pointer(&v.Func)).p, ok
}
func MethodByName(r reflect.Type, name string) (fn unsafe.Pointer, ok bool) {
	t := Create(r)
	if r.Kind() == reflect.Interface {
		return funcPointer(r.MethodByName(name))
	}
	ut := t.uncommon(r)
	if ut == nil {
		return nil, false
	}

	for _, p := range ut.methods() {
		if t.nameOff(p.name).name() == name {
			return t.Method(p), true
		}
	}
	return nil, false
}

func (t *rtype) Method(p method) (fn unsafe.Pointer) {
	tfn := t.textOff(p.tfn)
	fn = unsafe.Pointer(&tfn)
	return
}

type tflag uint8
type nameOff int32 // offset to a name
type typeOff int32 // offset to an *rtype
type textOff int32 // offset from top of text section

//go:linkname resolveTextOff reflect.resolveTextOff
func resolveTextOff(rtype unsafe.Pointer, off int32) unsafe.Pointer

func (t *rtype) textOff(off textOff) unsafe.Pointer {
	return resolveTextOff(unsafe.Pointer(t), int32(off))
}

//go:linkname resolveNameOff reflect.resolveNameOff
func resolveNameOff(ptrInModule unsafe.Pointer, off int32) unsafe.Pointer

func (t *rtype) nameOff(off nameOff) name {
	return name{(*byte)(resolveNameOff(unsafe.Pointer(t), int32(off)))}
}

const (
	tflagUncommon tflag = 1 << 0
)

// uncommonType is present only for defined types or types with methods
type uncommonType struct {
	pkgPath nameOff // import path; empty for built-in types like int, string
	mcount  uint16  // number of methods
	xcount  uint16  // number of exported methods
	moff    uint32  // offset from this uncommontype to [mcount]method
	_       uint32  // unused
}

// ptrType represents a pointer type.
type ptrType struct {
	rtype
	elem *rtype // pointer element (pointed at) type
}

// funcType represents a function type.
type funcType struct {
	rtype
	inCount  uint16
	outCount uint16 // top bit is set if last input parameter is ...
}

func add(p unsafe.Pointer, x uintptr, whySafe string) unsafe.Pointer {
	return unsafe.Pointer(uintptr(p) + x)
}

// interfaceType represents an interface type.
type interfaceType struct {
	rtype
	pkgPath name      // import path
	methods []imethod // sorted by hash
}

type imethod struct {
	name nameOff // name of method
	typ  typeOff // .(*FuncType) underneath
}

type String struct {
	Data unsafe.Pointer
	Len  int
}

func (t *rtype) uncommon(r reflect.Type) *uncommonType {
	if t.tflag&tflagUncommon == 0 {
		return nil
	}
	switch r.Kind() {
	case reflect.Ptr:
		type u struct {
			ptrType
			u uncommonType
		}
		return &(*u)(unsafe.Pointer(t)).u
	case reflect.Func:
		type u struct {
			funcType
			u uncommonType
		}
		return &(*u)(unsafe.Pointer(t)).u
	case reflect.Interface:
		type u struct {
			interfaceType
			u uncommonType
		}
		return &(*u)(unsafe.Pointer(t)).u
	case reflect.Struct:
		type u struct {
			interfaceType
			u uncommonType
		}
		return &(*u)(unsafe.Pointer(t)).u
	default:
		return nil
	}
}

// Method on non-interface type
type method struct {
	name nameOff // name of method
	mtyp typeOff // method type (without receiver)
	ifn  textOff // fn used in interface call (one-word receiver)
	tfn  textOff // fn used for normal method call
}

func (t *uncommonType) methods() []method {
	if t.mcount == 0 {
		return nil
	}
	return (*[1 << 16]method)(add(unsafe.Pointer(t), uintptr(t.moff), "t.mcount > 0"))[:t.mcount:t.mcount]
}
