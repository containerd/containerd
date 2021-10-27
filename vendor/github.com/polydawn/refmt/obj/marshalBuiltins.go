package obj

import (
	"reflect"

	. "github.com/polydawn/refmt/tok"
)

type ptrDerefDelegateMarshalMachine struct {
	MarshalMachine
	peelCount int

	isNil bool
}

func (mach *ptrDerefDelegateMarshalMachine) Reset(slab *marshalSlab, rv reflect.Value, _ reflect.Type) error {
	mach.isNil = false
	for i := 0; i < mach.peelCount; i++ {
		if rv.IsNil() {
			mach.isNil = true
			return nil
		}
		rv = rv.Elem()
	}
	return mach.MarshalMachine.Reset(slab, rv, rv.Type()) // REVIEW: we could have cached the peeled rt at mach conf time; worth it?
}
func (mach *ptrDerefDelegateMarshalMachine) Step(driver *Marshaller, slab *marshalSlab, tok *Token) (done bool, err error) {
	if mach.isNil {
		tok.Type = TNull
		return true, nil
	}
	return mach.MarshalMachine.Step(driver, slab, tok)
}

type marshalMachinePrimitive struct {
	kind reflect.Kind

	rv reflect.Value
}

func (mach *marshalMachinePrimitive) Reset(_ *marshalSlab, rv reflect.Value, _ reflect.Type) error {
	mach.rv = rv
	return nil
}
func (mach *marshalMachinePrimitive) Step(_ *Marshaller, _ *marshalSlab, tok *Token) (done bool, err error) {
	switch mach.kind {
	case reflect.Bool:
		tok.Type = TBool
		tok.Bool = mach.rv.Bool()
		return true, nil
	case reflect.String:
		tok.Type = TString
		tok.Str = mach.rv.String()
		return true, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		tok.Type = TInt
		tok.Int = mach.rv.Int()
		return true, nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		tok.Type = TUint
		tok.Uint = mach.rv.Uint()
		return true, nil
	case reflect.Float32, reflect.Float64:
		tok.Type = TFloat64
		tok.Float64 = mach.rv.Float()
		return true, nil
	case reflect.Slice: // implicitly bytes; no other slices are "primitive"
		if mach.rv.IsNil() {
			tok.Type = TNull
			return true, nil
		}
		tok.Type = TBytes
		tok.Bytes = mach.rv.Bytes()
		return true, nil
	case reflect.Array: // implicitly bytes; no other arrays are "primitive"
		tok.Type = TBytes
		// Unfortunately, there does not seem to be any efficient way to extract the contents of a byte array into a slice via reflect.
		// Since the lengths are part of the type, it is almost understandable that the stdlib reflect package has a hard time expressing this;
		// however, it drives me somewhat up the wall that they do not provide a case for arrays inside the `Value.Bytes` method, and panic.
		// Attempting to `Value.Convert(Type)` from a fixed-length array to a slice of the same type is also rejected.
		// Nor does `reflect.AppendSlice` accept an array kind as the second parameter; no, only slices there too.
		// So... we iterate.  If anyone knows a better way to do this, PRs extremely welcome.
		n := mach.rv.Len()
		tok.Bytes = make([]byte, n)
		for i := 0; i < n; i++ {
			tok.Bytes[i] = byte(mach.rv.Index(i).Uint())
		}
		return true, nil
	default:
		panic("unhandled")
	}
}
