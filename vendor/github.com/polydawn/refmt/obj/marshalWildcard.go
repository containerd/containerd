package obj

import (
	"reflect"

	. "github.com/polydawn/refmt/tok"
)

/*
	A MarshalMachine that unwraps an `interface{}` value,
	selects the correct machinery for handling its content,
	and delegates immediately to that machine.
*/
type marshalMachineWildcard struct {
	delegate MarshalMachine
}

func (mach *marshalMachineWildcard) Reset(slab *marshalSlab, rv reflect.Value, rt reflect.Type) error {
	// If the interface contains nil, go no further; we'll simply yield that single token.
	if rv.IsNil() {
		mach.delegate = nil
		return nil
	}
	// Pick, reset, and retain a delegate machine for the interior type.
	unwrap_rv := rv.Elem() // unwrap iface
	unwrap_rt := unwrap_rv.Type()
	mach.delegate = slab.requisitionMachine(unwrap_rt)
	return mach.delegate.Reset(slab, unwrap_rv, unwrap_rt)
}

func (mach marshalMachineWildcard) Step(driver *Marshaller, slab *marshalSlab, tok *Token) (done bool, err error) {
	if mach.delegate == nil {
		tok.Type = TNull
		return true, nil
	}
	return mach.delegate.Step(driver, slab, tok)
}
