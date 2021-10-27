package obj

import (
	"reflect"

	"github.com/polydawn/refmt/obj/atlas"
	. "github.com/polydawn/refmt/tok"
)

type marshalMachineTransform struct {
	trFunc   atlas.MarshalTransformFunc
	delegate MarshalMachine
	tagged   bool // Used to apply tag to first step (without forcing delegate to know).
	tag      int
	first    bool // This resets; 'tagged' persists (because it's type info).
}

func (mach *marshalMachineTransform) Reset(slab *marshalSlab, rv reflect.Value, _ reflect.Type) error {
	tr_rv, err := mach.trFunc(rv)
	if err != nil {
		return err
	}
	mach.first = true
	return mach.delegate.Reset(slab, tr_rv, tr_rv.Type())
}

func (mach *marshalMachineTransform) Step(driver *Marshaller, slab *marshalSlab, tok *Token) (done bool, err error) {
	done, err = mach.delegate.Step(driver, slab, tok)
	if mach.first && mach.tagged {
		tok.Tagged = true
		tok.Tag = mach.tag
		mach.first = false
	}
	return
}
