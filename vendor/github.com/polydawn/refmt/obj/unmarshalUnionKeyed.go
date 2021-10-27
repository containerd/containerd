package obj

import (
	"reflect"

	"github.com/polydawn/refmt/obj/atlas"
	. "github.com/polydawn/refmt/tok"
)

type unmarshalMachineUnionKeyed struct {
	cfg *atlas.UnionKeyedMorphism // set on initialization

	target_rv reflect.Value
	target_rt reflect.Type

	phase    unmarshalMachineUnionKeyedPhase
	tmp_rv   reflect.Value
	delegate UnmarshalMachine // actual machine, once we've demuxed with the second token (the key).
}

type unmarshalMachineUnionKeyedPhase uint8

const (
	unmarshalMachineUnionKeyedPhase_acceptMapOpen unmarshalMachineUnionKeyedPhase = iota
	unmarshalMachineUnionKeyedPhase_acceptKey
	unmarshalMachineUnionKeyedPhase_delegate
	unmarshalMachineUnionKeyedPhase_acceptMapClose
)

func (mach *unmarshalMachineUnionKeyed) Reset(_ *unmarshalSlab, rv reflect.Value, rt reflect.Type) error {
	mach.target_rv = rv
	mach.target_rt = rt
	mach.phase = unmarshalMachineUnionKeyedPhase_acceptMapOpen
	return nil
}

func (mach *unmarshalMachineUnionKeyed) Step(driver *Unmarshaller, slab *unmarshalSlab, tok *Token) (done bool, err error) {
	switch mach.phase {
	case unmarshalMachineUnionKeyedPhase_acceptMapOpen:
		return mach.step_acceptMapOpen(driver, slab, tok)
	case unmarshalMachineUnionKeyedPhase_acceptKey:
		return mach.step_acceptKey(driver, slab, tok)
	case unmarshalMachineUnionKeyedPhase_delegate:
		return mach.step_delegate(driver, slab, tok)
	case unmarshalMachineUnionKeyedPhase_acceptMapClose:
		return mach.step_acceptMapClose(driver, slab, tok)
	}
	panic("unreachable")
}

func (mach *unmarshalMachineUnionKeyed) step_acceptMapOpen(driver *Unmarshaller, slab *unmarshalSlab, tok *Token) (done bool, err error) {
	switch tok.Type {
	case TMapOpen:
		switch tok.Length {
		case -1: // pass
		case 1: // correct
		default:
			return true, ErrMalformedTokenStream{tok.Type, "unions in keyed format must be maps with exactly one entry"} // FIXME not malformed per se
		}
		mach.phase = unmarshalMachineUnionKeyedPhase_acceptKey
		return false, nil
	// REVIEW: is case TNull perhaps conditionally acceptable?
	default:
		return true, ErrMalformedTokenStream{tok.Type, "start of union value"} // FIXME not malformed per se
	}
}

func (mach *unmarshalMachineUnionKeyed) step_acceptKey(driver *Unmarshaller, slab *unmarshalSlab, tok *Token) (done bool, err error) {
	switch tok.Type {
	case TString:
		// Look up the configuration for this key.
		delegateAtlasEnt, ok := mach.cfg.Elements[tok.Str]
		if !ok {
			return true, ErrNoSuchUnionMember{tok.Str, mach.target_rt, mach.cfg.KnownMembers}
		}
		// Allocate a new concrete value, and hang on to that rv handle.
		//  Assigning into the interface must be done at the end if it's a non-pointer.
		mach.tmp_rv = reflect.New(delegateAtlasEnt.Type).Elem()
		// Get and configure a machine for the delegation.
		delegate := _yieldUnmarshalMachinePtrForAtlasEntry(slab.tip(), delegateAtlasEnt, slab.atlas)
		if err := delegate.Reset(slab, mach.tmp_rv, delegateAtlasEnt.Type); err != nil {
			return true, err
		}
		mach.delegate = delegate
		mach.phase = unmarshalMachineUnionKeyedPhase_delegate
		return false, nil
	default:
		return true, ErrMalformedTokenStream{tok.Type, "map key"}
	}
}

func (mach *unmarshalMachineUnionKeyed) step_delegate(driver *Unmarshaller, slab *unmarshalSlab, tok *Token) (done bool, err error) {
	done, err = mach.delegate.Step(driver, slab, tok)
	if done && err == nil {
		mach.phase = unmarshalMachineUnionKeyedPhase_acceptMapClose
		return false, nil
	}
	return
}

func (mach *unmarshalMachineUnionKeyed) step_acceptMapClose(driver *Unmarshaller, slab *unmarshalSlab, tok *Token) (done bool, err error) {
	switch tok.Type {
	case TMapClose:
		mach.target_rv.Set(mach.tmp_rv)
		return true, nil
	default:
		return true, ErrMalformedTokenStream{tok.Type, "map close at end of union value"}
	}
}
