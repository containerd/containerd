package obj

import (
	"fmt"
	"reflect"

	"github.com/polydawn/refmt/obj/atlas"
	. "github.com/polydawn/refmt/tok"
)

type marshalMachineUnionKeyed struct {
	cfg *atlas.AtlasEntry // set on initialization

	target_rv   reflect.Value // the element (interface already unwrapped).
	elementName string        // the serial name for this union member type.

	step     marshalMachineStep
	delegate MarshalMachine // actual machine, picked based on content of the interface.
}

func (mach *marshalMachineUnionKeyed) Reset(slab *marshalSlab, rv reflect.Value, rt reflect.Type) error {
	mach.target_rv = rv.Elem()
	if mach.target_rv.Kind() == reflect.Invalid {
		return fmt.Errorf("nil is not a valid member for the union for interface %q", mach.cfg.Type.Name())
	}
	element_rt := mach.target_rv.Type()
	mach.elementName = mach.cfg.UnionKeyedMorphism.Mappings[reflect.ValueOf(element_rt).Pointer()]
	if mach.elementName == "" {
		return fmt.Errorf("type %q is not one of the known members of the union for interface %q", element_rt.Name(), mach.cfg.Type.Name())
	}
	delegateAtlasEnt := mach.cfg.UnionKeyedMorphism.Elements[mach.elementName]
	mach.delegate = _yieldMarshalMachinePtrForAtlasEntry(slab.tip(), delegateAtlasEnt, slab.atlas)
	if err := mach.delegate.Reset(slab, mach.target_rv, delegateAtlasEnt.Type); err != nil {
		return err
	}
	mach.step = mach.step_emitMapOpen
	return nil
}

func (mach *marshalMachineUnionKeyed) Step(driver *Marshaller, slab *marshalSlab, tok *Token) (done bool, err error) {
	return mach.step(driver, slab, tok)
}

func (mach *marshalMachineUnionKeyed) step_emitMapOpen(driver *Marshaller, slab *marshalSlab, tok *Token) (done bool, err error) {
	tok.Type = TMapOpen
	tok.Length = 1
	mach.step = mach.step_emitKey
	return false, nil
}

func (mach *marshalMachineUnionKeyed) step_emitKey(driver *Marshaller, slab *marshalSlab, tok *Token) (done bool, err error) {
	tok.Type = TString
	tok.Str = mach.elementName
	mach.step = mach.step_delegate
	return false, nil
}

func (mach *marshalMachineUnionKeyed) step_delegate(driver *Marshaller, slab *marshalSlab, tok *Token) (done bool, err error) {
	done, err = mach.delegate.Step(driver, slab, tok)
	if done && err == nil {
		mach.step = mach.step_emitMapClose
		return false, nil
	}
	return
}

func (mach *marshalMachineUnionKeyed) step_emitMapClose(driver *Marshaller, slab *marshalSlab, tok *Token) (done bool, err error) {
	tok.Type = TMapClose
	mach.step = nil
	return true, nil
}
