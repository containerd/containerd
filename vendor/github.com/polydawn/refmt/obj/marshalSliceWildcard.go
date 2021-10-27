package obj

import (
	"fmt"
	"reflect"

	. "github.com/polydawn/refmt/tok"
)

// Encodes a slice.
// This machine just wraps the array machine, checking to make sure the value isn't nil.
type marshalMachineSliceWildcard struct {
	marshalMachineArrayWildcard
}

func (mach *marshalMachineSliceWildcard) Step(driver *Marshaller, slab *marshalSlab, tok *Token) (done bool, err error) {
	if mach.index < 0 {
		if mach.target_rv.IsNil() {
			tok.Type = TNull
			return true, nil
		}
	}
	return mach.marshalMachineArrayWildcard.Step(driver, slab, tok)
}

type marshalMachineArrayWildcard struct {
	target_rv reflect.Value
	value_rt  reflect.Type
	valueMach MarshalMachine
	index     int
	length    int
}

func (mach *marshalMachineArrayWildcard) Reset(slab *marshalSlab, rv reflect.Value, rt reflect.Type) error {
	mach.target_rv = rv
	mach.value_rt = rt.Elem()
	mach.valueMach = slab.requisitionMachine(mach.value_rt)
	mach.index = -1
	mach.length = mach.target_rv.Len()
	return nil
}

func (mach *marshalMachineArrayWildcard) Step(driver *Marshaller, slab *marshalSlab, tok *Token) (done bool, err error) {
	if mach.index < 0 {
		tok.Type = TArrOpen
		tok.Length = mach.target_rv.Len()
		mach.index++
		return false, nil
	}
	if mach.index == mach.length {
		tok.Type = TArrClose
		mach.index++
		slab.release()
		return true, nil
	}
	if mach.index > mach.length {
		return true, fmt.Errorf("invalid state: value already consumed")
	}
	rv := mach.target_rv.Index(mach.index)
	driver.Recurse(tok, rv, mach.value_rt, mach.valueMach)
	mach.index++
	return false, nil
}
