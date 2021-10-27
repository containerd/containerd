package obj

import (
	"fmt"
	"reflect"

	. "github.com/polydawn/refmt/tok"
)

type unmarshalMachineWildcard struct {
	target_rv reflect.Value
	target_rt reflect.Type
	delegate  UnmarshalMachine // actual machine, once we've demuxed with the first token.
	holder_rv reflect.Value    // if set, handle to slot where slice is stored; content must be placed into target at end.
}

func (mach *unmarshalMachineWildcard) Reset(_ *unmarshalSlab, rv reflect.Value, rt reflect.Type) error {
	mach.target_rv = rv
	mach.target_rt = rt
	mach.delegate = nil
	mach.holder_rv = reflect.Value{}
	return nil
}

func (mach *unmarshalMachineWildcard) Step(driver *Unmarshaller, slab *unmarshalSlab, tok *Token) (done bool, err error) {
	if mach.delegate == nil {
		done, err = mach.prepareDemux(driver, slab, tok)
		if done {
			return
		}
	}
	done, err = mach.delegate.Step(driver, slab, tok)
	if !done {
		return
	}
	if mach.holder_rv.IsValid() {
		mach.target_rv.Set(mach.holder_rv)
	}
	return
}

func (mach *unmarshalMachineWildcard) prepareDemux(driver *Unmarshaller, slab *unmarshalSlab, tok *Token) (done bool, err error) {
	// If a "tag" is set in the token, we try to follow that as a hint for
	//  any specifically customized behaviors for how this should be unmarshalled.
	if tok.Tagged == true {
		atlasEntry, exists := slab.atlas.GetEntryByTag(tok.Tag)
		if !exists {
			return true, fmt.Errorf("missing an unmarshaller for tag %v", tok.Tag)
		}
		value_rt := atlasEntry.Type
		mach.holder_rv = reflect.New(value_rt).Elem()
		mach.delegate = _yieldUnmarshalMachinePtr(slab.tip(), slab.atlas, value_rt)
		if err := mach.delegate.Reset(slab, mach.holder_rv, value_rt); err != nil {
			return true, err
		}
		return false, nil
	}

	// Switch on token type: we may be able to delegate to a primitive machine,
	//  but we may also need to initialize a container type and then hand off.
	switch tok.Type {
	case TMapOpen:
		child := make(map[string]interface{})
		child_rv := reflect.ValueOf(child)
		mach.target_rv.Set(child_rv)
		mach.delegate = &slab.tip().unmarshalMachineMapStringWildcard
		if err := mach.delegate.Reset(slab, child_rv, child_rv.Type()); err != nil {
			return true, err
		}
		return false, nil

	case TArrOpen:
		// Stdlib has very interesting branch here: 'case reflect.Interface: if v.NumMethod() == 0 {'
		//  If that matches, it goes on a *totally different* branch that leaves the reflective path entirely forever.
		//   (Which is kind of interesting, because it also means it will never reuse memory there.  If you wanted that.)

		// This definitely went through a few discovery steps...
		// - https://play.golang.org/p/Qbtpxwh68e
		// - https://play.golang.org/p/l5RQujLnDN
		// - https://play.golang.org/p/Z2ilpPk0vk
		// - https://play.golang.org/p/jV9VFDht6F -- finally getting somewhere good

		holder := make([]interface{}, 0)
		mach.holder_rv = reflect.ValueOf(&holder).Elem()
		mach.delegate = &slab.tip().unmarshalMachineSliceWildcard
		if err := mach.delegate.Reset(slab, mach.holder_rv, mach.holder_rv.Type()); err != nil {
			return true, err
		}
		return false, nil

	case TMapClose:
		return true, ErrMalformedTokenStream{tok.Type, "start of value"}

	case TArrClose:
		return true, ErrMalformedTokenStream{tok.Type, "start of value"}

	case TNull:
		mach.target_rv.Set(reflect.Zero(mach.target_rt))
		return true, nil

	default:
		// If it wasn't the start of composite, shell out to the machine for literals.
		// Don't bother to replace our internal step func because literal machines are never multi-call,
		//  and this lets us avoid grabbing a pointer and it shuffling around.
		delegateMach := slab.tip().unmarshalMachinePrimitive
		delegateMach.kind = reflect.Interface
		if err := delegateMach.Reset(slab, mach.target_rv, nil); err != nil {
			return true, err
		}
		return delegateMach.Step(driver, slab, tok)
	}
}
