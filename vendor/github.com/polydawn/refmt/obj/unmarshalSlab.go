package obj

import (
	"fmt"
	"reflect"

	"github.com/polydawn/refmt/obj/atlas"
	. "github.com/polydawn/refmt/tok"
)

/*
	A lovely mechanism to stash unmarshalMachine objects pre-allocated and avoid mallocs.
	Works together with the Atlas: the Atlas says what kind of machinery is needed;
	the unmarshalSlab "allocates" it and returns it upon your request.
*/
type unmarshalSlab struct {
	atlas atlas.Atlas
	rows  []unmarshalSlabRow
}

type unmarshalSlabRow struct {
	ptrDerefDelegateUnmarshalMachine
	unmarshalMachinePrimitive
	unmarshalMachineWildcard
	unmarshalMachineMapStringWildcard
	unmarshalMachineSliceWildcard
	unmarshalMachineArrayWildcard
	unmarshalMachineStructAtlas
	unmarshalMachineTransform
	unmarshalMachineUnionKeyed

	errThunkUnmarshalMachine
}

/*
	Return a reference to a machine from the slab.
	*You must release() when done.*

	Errors -- including "no info in Atlas for this type" -- are expressed by
	returning a machine that is a constantly-erroring thunk.
*/
func (slab *unmarshalSlab) requisitionMachine(rt reflect.Type) UnmarshalMachine {
	// Acquire a row.
	off := len(slab.rows)
	slab.grow()
	row := &slab.rows[off]

	// Indirect pointers as necessary.
	//  Keep count of how many times we do this; we'll use this again at the end.
	peelCount := 0
	for rt.Kind() == reflect.Ptr {
		rt = rt.Elem()
		peelCount++
	}

	// Figure out what machinery to use at heart.
	mach := _yieldUnmarshalMachinePtr(row, slab.atlas, rt)
	// If nil answer, we had no match: yield an error thunk.
	if mach == nil {
		mach := &row.errThunkUnmarshalMachine
		mach.err = fmt.Errorf("no machine found")
		return mach
	}

	// If no indirection steps, return;
	//  otherwise wrap it in the ptrDeref machine and return that.
	if peelCount == 0 {
		return mach
	}
	row.ptrDerefDelegateUnmarshalMachine.UnmarshalMachine = mach
	row.ptrDerefDelegateUnmarshalMachine.peelCount = peelCount
	return &row.ptrDerefDelegateUnmarshalMachine
}

func _yieldUnmarshalMachinePtr(row *unmarshalSlabRow, atl atlas.Atlas, rt reflect.Type) UnmarshalMachine {
	rtid := reflect.ValueOf(rt).Pointer()

	// Check primitives first; cheapest (and unoverridable).
	switch rtid {
	case rtid_bool,
		rtid_string,
		rtid_int, rtid_int8, rtid_int16, rtid_int32, rtid_int64,
		rtid_uint, rtid_uint8, rtid_uint16, rtid_uint32, rtid_uint64, rtid_uintptr,
		rtid_float32, rtid_float64,
		rtid_bytes:
		row.unmarshalMachinePrimitive.kind = rt.Kind()
		return &row.unmarshalMachinePrimitive
	}

	// Consult atlas second.
	if entry, ok := atl.Get(rtid); ok {
		return _yieldUnmarshalMachinePtrForAtlasEntry(row, entry, atl)
	}

	// If no specific behavior found, use default behavior based on kind.
	switch rt.Kind() {

	case reflect.Bool,
		reflect.String,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64:
		row.unmarshalMachinePrimitive.kind = rt.Kind()
		return &row.unmarshalMachinePrimitive
	case reflect.Slice:
		// un-typedef'd byte slices were handled already, but a typedef'd one still gets gets treated like a special kind:
		if rt.Elem().Kind() == reflect.Uint8 {
			row.unmarshalMachinePrimitive.kind = rt.Kind()
			return &row.unmarshalMachinePrimitive
		}
		return &row.unmarshalMachineSliceWildcard
	case reflect.Array:
		// arrays of bytes have a similar special case to slices for when they're typedefed.
		if rt.Elem().Kind() == reflect.Uint8 {
			row.unmarshalMachinePrimitive.kind = rt.Kind()
			return &row.unmarshalMachinePrimitive
		}
		return &row.unmarshalMachineArrayWildcard
	case reflect.Map:
		return &row.unmarshalMachineMapStringWildcard
	case reflect.Struct:
		// TODO here we could also invoke automatic atlas autogen, if configured to be permitted
		mach := &row.errThunkUnmarshalMachine
		mach.err = fmt.Errorf("missing an atlas entry describing how to unmarshal type %v (and auto-atlasing for structs is not enabled)", rt)
		return mach
	case reflect.Interface:
		return &row.unmarshalMachineWildcard
	case reflect.Func:
		panic(fmt.Errorf("functions cannot be unmarshalled!"))
	case reflect.Ptr:
		panic(fmt.Errorf("unreachable: ptrs must already be resolved"))
	default:
		panic(fmt.Errorf("excursion %s", rt.Kind()))
	}
}

// given that we already have an atlasEntry in mind, yield a configured machine for it.
// it seems odd that this might still require a whole atlas, but tis so;
// some things (e.g. transform funcs) need to get additional machinery for delegation.
func _yieldUnmarshalMachinePtrForAtlasEntry(row *unmarshalSlabRow, entry *atlas.AtlasEntry, atl atlas.Atlas) UnmarshalMachine {
	// Switch across which of the union of configurations is applicable.
	switch {
	case entry.UnmarshalTransformFunc != nil:
		// Return a machine that calls the func(s), then later a real machine.
		// The entry.UnmarshalTransformTargetType is used to do a recursive lookup.
		// We can't just call the func here because we're still working off typeinfo
		// and don't have a real value to transform until later.
		row.unmarshalMachineTransform.trFunc = entry.UnmarshalTransformFunc
		row.unmarshalMachineTransform.recv_rt = entry.UnmarshalTransformTargetType
		// Pick delegate without growing stack.  (This currently means recursive transform won't fly.)
		row.unmarshalMachineTransform.delegate = _yieldUnmarshalMachinePtr(row, atl, entry.UnmarshalTransformTargetType)
		return &row.unmarshalMachineTransform
	case entry.StructMap != nil:
		row.unmarshalMachineStructAtlas.cfg = entry
		return &row.unmarshalMachineStructAtlas
	case entry.UnionKeyedMorphism != nil:
		row.unmarshalMachineUnionKeyed.cfg = entry.UnionKeyedMorphism
		return &row.unmarshalMachineUnionKeyed
	default:
		panic("invalid atlas entry")
	}
}

// Returns the top row of the slab.  Useful for machines that need to delegate
//  to another type that's definitely not their own (comes up for the wildcard delegators).
func (s *unmarshalSlab) tip() *unmarshalSlabRow {
	return &s.rows[len(s.rows)-1]
}

func (s *unmarshalSlab) grow() {
	s.rows = append(s.rows, unmarshalSlabRow{})
}

func (s *unmarshalSlab) release() {
	s.rows = s.rows[0 : len(s.rows)-1]
}

type errThunkUnmarshalMachine struct {
	err error
}

func (m *errThunkUnmarshalMachine) Reset(_ *unmarshalSlab, _ reflect.Value, _ reflect.Type) error {
	return m.err
}
func (m *errThunkUnmarshalMachine) Step(d *Unmarshaller, s *unmarshalSlab, tok *Token) (done bool, err error) {
	return true, m.err
}
