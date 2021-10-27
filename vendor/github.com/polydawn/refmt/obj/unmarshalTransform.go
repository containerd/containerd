package obj

import (
	"reflect"

	"github.com/polydawn/refmt/obj/atlas"
	. "github.com/polydawn/refmt/tok"
)

type unmarshalMachineTransform struct {
	trFunc   atlas.UnmarshalTransformFunc
	recv_rt  reflect.Type
	delegate UnmarshalMachine // machine for handling the recv type, stepped to completion before transform applied.

	target_rv reflect.Value // given on Reset, retained until last step, and set into after using trFunc
	recv_rv   reflect.Value // if set, handle to slot where slice is stored; content must be placed into target at end.
}

func (mach *unmarshalMachineTransform) Reset(slab *unmarshalSlab, rv reflect.Value, _ reflect.Type) error {
	mach.target_rv = rv
	mach.recv_rv = reflect.New(mach.recv_rt).Elem() // REVIEW: this behavior with ptr vs not for in_rt.  the star-star case is prob not what want.
	return mach.delegate.Reset(slab, mach.recv_rv, mach.recv_rt)
}

func (mach *unmarshalMachineTransform) Step(driver *Unmarshaller, slab *unmarshalSlab, tok *Token) (done bool, err error) {
	done, err = mach.delegate.Step(driver, slab, tok)
	if err != nil {
		return
	}
	if !done {
		return
	}
	// on the last step, use transform, and finally set in real target.
	tr_rv, err := mach.trFunc(mach.recv_rv)
	// do attempt the set even if error.  user may appreciate partial progress.
	mach.target_rv.Set(tr_rv)
	return true, err
}
