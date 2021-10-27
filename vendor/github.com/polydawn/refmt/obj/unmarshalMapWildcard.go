package obj

import (
	"fmt"
	"reflect"

	"github.com/polydawn/refmt/obj/atlas"
	. "github.com/polydawn/refmt/tok"
)

type unmarshalMachineMapStringWildcard struct {
	target_rv     reflect.Value                // Handle to the map.  Can set to zero, or set k=v pairs into, etc.
	value_rt      reflect.Type                 // Type info for map values (cached for convenience in recurse calls).
	valueMach     UnmarshalMachine             // Machine for map values.
	valueZero_rv  reflect.Value                // Cached instance of the zero value of the value type, for re-zeroing tmp_rv.
	key_rv        reflect.Value                // Addressable handle to a slot for keys to unmarshal into.
	keyDestringer atlas.UnmarshalTransformFunc // Transform str->foo, to be used if keys are not plain strings.
	tmp_rv        reflect.Value                // Addressable handle to a slot for values to unmarshal into.
	phase         unmarshalMachineMapStringWildcardPhase
}

type unmarshalMachineMapStringWildcardPhase uint8

const (
	unmarshalMachineMapStringWildcardPhase_initial          unmarshalMachineMapStringWildcardPhase = iota
	unmarshalMachineMapStringWildcardPhase_acceptKeyOrClose                                        // doesn't commit prev value
	unmarshalMachineMapStringWildcardPhase_acceptValue
	unmarshalMachineMapStringWildcardPhase_acceptAnotherKeyOrClose
)

func (mach *unmarshalMachineMapStringWildcard) Reset(slab *unmarshalSlab, rv reflect.Value, rt reflect.Type) error {
	mach.target_rv = rv
	mach.value_rt = rt.Elem()
	mach.valueMach = slab.requisitionMachine(mach.value_rt)
	mach.valueZero_rv = reflect.Zero(mach.value_rt)
	key_rt := rt.Key()
	mach.key_rv = reflect.New(key_rt).Elem()
	if mach.key_rv.Kind() != reflect.String {
		rtid := reflect.ValueOf(key_rt).Pointer()
		atlEnt, ok := slab.atlas.Get(rtid)
		if !ok || atlEnt.UnmarshalTransformTargetType == nil || atlEnt.UnmarshalTransformTargetType.Kind() != reflect.String {
			return fmt.Errorf("unsupported map key type %q (if you want to use struct keys, your atlas needs a transform from string)", key_rt.Name())
		}
		mach.keyDestringer = atlEnt.UnmarshalTransformFunc
	}
	mach.tmp_rv = reflect.New(mach.value_rt).Elem()
	mach.phase = unmarshalMachineMapStringWildcardPhase_initial
	return nil
}

func (mach *unmarshalMachineMapStringWildcard) Step(driver *Unmarshaller, slab *unmarshalSlab, tok *Token) (done bool, err error) {
	switch mach.phase {
	case unmarshalMachineMapStringWildcardPhase_initial:
		return mach.step_Initial(driver, slab, tok)
	case unmarshalMachineMapStringWildcardPhase_acceptKeyOrClose:
		return mach.step_AcceptKeyOrClose(driver, slab, tok)
	case unmarshalMachineMapStringWildcardPhase_acceptValue:
		return mach.step_AcceptValue(driver, slab, tok)
	case unmarshalMachineMapStringWildcardPhase_acceptAnotherKeyOrClose:
		return mach.step_AcceptAnotherKeyOrClose(driver, slab, tok)
	}
	panic("unreachable")
}

func (mach *unmarshalMachineMapStringWildcard) step_Initial(_ *Unmarshaller, _ *unmarshalSlab, tok *Token) (done bool, err error) {
	// If it's a special state, start an object.
	//  (Or, blow up if its a special state that's silly).
	switch tok.Type {
	case TNull:
		mach.target_rv.Set(reflect.Zero(mach.target_rv.Type()))
		return true, nil
	case TMapOpen:
		// Great.  Consumed.
		mach.phase = unmarshalMachineMapStringWildcardPhase_acceptKeyOrClose
		// Initialize the map if it's nil.
		if mach.target_rv.IsNil() {
			mach.target_rv.Set(reflect.MakeMap(mach.target_rv.Type()))
		}
		return false, nil
	case TMapClose:
		return true, fmt.Errorf("unexpected mapClose; expected start of map")
	case TArrClose:
		return true, fmt.Errorf("unexpected arrClose; expected start of map")
	case TArrOpen:
		fallthrough
	default:
		return true, ErrUnmarshalTypeCantFit{*tok, mach.target_rv, 0}
	}
}

func (mach *unmarshalMachineMapStringWildcard) step_AcceptKeyOrClose(_ *Unmarshaller, slab *unmarshalSlab, tok *Token) (done bool, err error) {
	// Switch on tokens.
	switch tok.Type {
	case TMapOpen:
		return true, fmt.Errorf("unexpected mapOpen; expected map key")
	case TArrOpen:
		return true, fmt.Errorf("unexpected arrOpen; expected map key")
	case TMapClose:
		// no special checks for ends of wildcard map; no such thing as incomplete.
		// release the slab row we requisitioned for our value machine.
		slab.release()
		return true, nil
	case TArrClose:
		return true, fmt.Errorf("unexpected arrClose; expected map key")
	case TString:
		if mach.keyDestringer != nil {
			key_rv, err := mach.keyDestringer(reflect.ValueOf(tok.Str))
			if err != nil {
				return true, fmt.Errorf("unsupported map key type %q: errors in stringifying: %s", mach.key_rv.Type().Name(), err)
			}
			mach.key_rv.Set(key_rv)
		} else {
			mach.key_rv.SetString(tok.Str)
		}
		if err = mach.mustAcceptKey(mach.key_rv); err != nil {
			return true, err
		}
		mach.phase = unmarshalMachineMapStringWildcardPhase_acceptValue
		return false, nil
	default:
		return true, fmt.Errorf("unexpected token %s; expected key string or end of map", tok)
	}
}

func (mach *unmarshalMachineMapStringWildcard) mustAcceptKey(key_rv reflect.Value) error {
	if exists := mach.target_rv.MapIndex(key_rv).IsValid(); exists {
		return fmt.Errorf("repeated key %q", key_rv)
	}
	return nil
}

func (mach *unmarshalMachineMapStringWildcard) step_AcceptValue(driver *Unmarshaller, slab *unmarshalSlab, tok *Token) (done bool, err error) {
	mach.phase = unmarshalMachineMapStringWildcardPhase_acceptAnotherKeyOrClose
	mach.tmp_rv.Set(mach.valueZero_rv)
	return false, driver.Recurse(
		tok,
		mach.tmp_rv,
		mach.value_rt,
		mach.valueMach,
	)
}

func (mach *unmarshalMachineMapStringWildcard) step_AcceptAnotherKeyOrClose(_ *Unmarshaller, slab *unmarshalSlab, tok *Token) (done bool, err error) {
	// First, save any refs from the last value.
	//  (This is fiddly: the delay comes mostly from the handling of slices, which may end up re-allocating
	//   themselves during their decoding.)
	mach.target_rv.SetMapIndex(mach.key_rv, mach.tmp_rv)

	// The rest is the same as the very first acceptKeyOrClose (and has the same future state transitions).
	return mach.step_AcceptKeyOrClose(nil, slab, tok)
}
