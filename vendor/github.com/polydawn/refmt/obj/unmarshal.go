package obj

import (
	"reflect"

	"github.com/polydawn/refmt/obj/atlas"
	. "github.com/polydawn/refmt/tok"
)

/*
	Allocates the machinery for treating an in-memory object like a `TokenSink`.
	This machinery will walk over values,	using received tokens to fill in
	fields as it visits them.

	Initialization must be finished by calling `Bind` to set the value to visit;
	after this, the `Step` function is ready to be pumped.
	Subsequent calls to `Bind` do a full reset, leaving `Step` ready to call
	again and making all of the machinery reusable without re-allocating.
*/
func NewUnmarshaller(atl atlas.Atlas) *Unmarshaller {
	d := &Unmarshaller{
		unmarshalSlab: unmarshalSlab{
			atlas: atl,
			rows:  make([]unmarshalSlabRow, 0, 10),
		},
		stack: make([]UnmarshalMachine, 0, 10),
	}
	return d
}

func (d *Unmarshaller) Bind(v interface{}) error {
	d.stack = d.stack[0:0]
	d.unmarshalSlab.rows = d.unmarshalSlab.rows[0:0]
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		err := ErrInvalidUnmarshalTarget{reflect.TypeOf(v)}
		d.step = &errThunkUnmarshalMachine{err}
		return err
	}
	rv = rv.Elem() // Let's just always be addressible, shall we?
	rt := rv.Type()
	d.step = d.unmarshalSlab.requisitionMachine(rt)
	return d.step.Reset(&d.unmarshalSlab, rv, rt)
}

type Unmarshaller struct {
	unmarshalSlab unmarshalSlab
	stack         []UnmarshalMachine
	step          UnmarshalMachine
}

type UnmarshalMachine interface {
	Reset(*unmarshalSlab, reflect.Value, reflect.Type) error
	Step(*Unmarshaller, *unmarshalSlab, *Token) (done bool, err error)
}

func (d *Unmarshaller) Step(tok *Token) (bool, error) {
	done, err := d.step.Step(d, &d.unmarshalSlab, tok)
	// If the step errored: out, entirely.
	if err != nil {
		return true, err
	}
	// If the step wasn't done, return same status.
	if !done {
		return false, nil
	}
	// If it WAS done, pop next, or if stack empty, we're entirely done.
	nSteps := len(d.stack) - 1
	if nSteps == -1 {
		return true, nil // that's all folks
	}
	d.step = d.stack[nSteps]
	d.stack = d.stack[0:nSteps]
	return false, nil
}

/*
	Starts the process of recursing unmarshalling over value `rv`.

	Caller provides the machine to use (this is an optimization for maps and slices,
	which already know the machine and keep reusing it for all their entries).
	This method pushes the first step with `tok` (the upstream tends to have peeked at
	it in order to decide what to do, but if recursing, it belongs to the next obj),
	then saves this new machine onto the driver's stack: future calls to step
	the driver will then continuing stepping the new machine it returns a done status,
	at which point we'll finally "return" by popping back to the last machine on the stack
	(which is presumably the same one that just called this Recurse method).

	In other words, your UnmarshalMachine calls this when it wants to deal
	with an object, and by the time we call back to your machine again,
	that object will be traversed and the stream ready for you to continue.
*/
func (d *Unmarshaller) Recurse(tok *Token, rv reflect.Value, rt reflect.Type, nextMach UnmarshalMachine) (err error) {
	//	fmt.Printf(">>> pushing into recursion with %#v\n", nextMach)
	// Push the current machine onto the stack (we'll resume it when the new one is done),
	d.stack = append(d.stack, d.step)
	// Initialize the machine for this new target value.
	err = nextMach.Reset(&d.unmarshalSlab, rv, rt)
	if err != nil {
		return
	}
	d.step = nextMach
	// Immediately make a step (we're still the delegate in charge of someone else's step).
	_, err = d.Step(tok)
	return
}
