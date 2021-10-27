package obj

import (
	"reflect"

	. "github.com/polydawn/refmt/tok"
)

type unmarshalMachineSliceWildcard struct {
	target_rv    reflect.Value // target slice handle
	working_rv   reflect.Value // working slice (target is set to this at end)
	value_rt     reflect.Type
	valueZero_rv reflect.Value // a zero of the slice's value type (for kicking append)
	valueMach    UnmarshalMachine
	phase        unmarshalMachineArrayWildcardPhase
	index        int
}

func (mach *unmarshalMachineSliceWildcard) Reset(slab *unmarshalSlab, rv reflect.Value, rt reflect.Type) error {
	mach.target_rv = rv
	mach.working_rv = rv
	mach.value_rt = rt.Elem()
	mach.valueZero_rv = reflect.Zero(mach.value_rt)
	mach.valueMach = slab.requisitionMachine(mach.value_rt)
	mach.phase = unmarshalMachineArrayWildcardPhase_initial
	mach.index = 0
	return nil
}

func (mach *unmarshalMachineSliceWildcard) Step(driver *Unmarshaller, slab *unmarshalSlab, tok *Token) (done bool, err error) {
	switch mach.phase {
	case unmarshalMachineArrayWildcardPhase_initial:
		return mach.step_Initial(driver, slab, tok)
	case unmarshalMachineArrayWildcardPhase_acceptValueOrClose:
		return mach.step_AcceptValueOrClose(driver, slab, tok)
	}
	panic("unreachable")
}

func (mach *unmarshalMachineSliceWildcard) step_Initial(_ *Unmarshaller, slab *unmarshalSlab, tok *Token) (done bool, err error) {
	// If it's a special state, start an object.
	//  (Or, blow up if its a special state that's silly).
	switch tok.Type {
	case TMapOpen:
		return true, ErrMalformedTokenStream{tok.Type, "start of array"}
	case TArrOpen:
		// Great.  Consumed.
		mach.phase = unmarshalMachineArrayWildcardPhase_acceptValueOrClose
		// Initialize the slice.
		mach.target_rv.Set(reflect.MakeSlice(mach.target_rv.Type(), 0, 0))
		return false, nil
	case TMapClose:
		return true, ErrMalformedTokenStream{tok.Type, "start of array"}
	case TArrClose:
		return true, ErrMalformedTokenStream{tok.Type, "start of array"}
	case TNull:
		mach.target_rv.Set(reflect.Zero(mach.target_rv.Type()))
		return true, nil
	default:
		return true, ErrMalformedTokenStream{tok.Type, "start of array"}
	}
}

func (mach *unmarshalMachineSliceWildcard) step_AcceptValueOrClose(driver *Unmarshaller, slab *unmarshalSlab, tok *Token) (done bool, err error) {
	// Either form of open token are valid, but
	// - an arrClose is ours
	// - and a mapClose is clearly invalid.
	switch tok.Type {
	case TMapClose:
		// no special checks for ends of wildcard slice; no such thing as incomplete.
		return true, ErrMalformedTokenStream{tok.Type, "start of value or end of array"}
	case TArrClose:
		mach.target_rv.Set(mach.working_rv)
		// release the slab row we requisitioned for our value machine.
		slab.release()
		return true, nil
	}

	// Grow the slice if necessary.
	mach.working_rv = reflect.Append(mach.working_rv, mach.valueZero_rv)

	// Recurse on a handle to the next index.
	rv := mach.working_rv.Index(mach.index)
	mach.index++
	return false, driver.Recurse(tok, rv, mach.value_rt, mach.valueMach)
	// Step simply remains `step_AcceptValueOrClose` -- arrays don't have much state machine.
}
