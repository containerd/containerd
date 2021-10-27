package atlas

import (
	"reflect"
)

/*
	The AtlasEntry is a declarative roadmap of what we should do for
	marshal and unmarshal of a single object, keyed by type.

	There are a lot of paths your mappings might want to take:

	  - For a struct type, you may simply want to specify some alternate keys, or some to leave out, etc.
	  - For an interface type, you probably want to specify one of our interface muxing strategies
	     with a mapping between enumstr:typeinfo (and, what to do if we get a struct we don't recognize).
	  - For a string, int, or other primitive, you don't need to say anything: defaults will DTRT.
	  - For a typedef'd string, int, or other primitive, you *still* don't need to say anything: but,
	     if you want custom behavior (say, transform the string to an int at the last second, and back again),
		 you can specify transformer functions for that.
	  - For a struct type that you want to turn into a whole different kind (like a string): use
	     those same transform functions.  (You'll no longer need a FieldMap.)
	  - For the most esoteric needs, you can fall all the way back to providing a custom MarshalMachine
	     (but avoid that; it's a lot of work, and one of these other transform methods should suffice).
*/
type AtlasEntry struct {
	// The reflect info of the type this morphism is regarding.
	Type reflect.Type

	// --------------------------------------------------------
	// The big escape valves: wanna map to some other kind completely?
	// --------------------------------------------------------

	// Transforms the value we reached by walking (the 'live' value -- which
	// must be of `this.Type`) into another value (the 'serialable' value --
	// which will be of `this.MarshalTransformTargetType`).
	//
	// The target type may be anything, even of a completely different Kind!
	//
	// This transform func runs first, then the resulting value is
	// serialized (by running through the path through Atlas again, so
	// chaining of transform funcs is supported, though not recommended).
	MarshalTransformFunc MarshalTransformFunc
	// The type of value we expect after using the MarshalTransformFunc.
	//
	// The match between transform func and target type should be checked
	// during construction of this AtlasEntry.
	MarshalTransformTargetType reflect.Type

	// Expects a different type (the 'serialable' value -- which will be of
	// 'this.UnmarshalTransformTargetType') than the value we reached by
	// walking (the 'live' value -- which must be of `this.Type`).
	//
	// The target type may be anything, even of a completely different Kind!
	//
	// The unmarshal of that target type will be run first, then the
	// resulting value is fed through this function to produce the real value,
	// which is then placed correctly into bigger mid-unmarshal object tree.
	//
	// For non-primitives, unmarshal of the target type will always target
	// an empty pointer or empty slice, roughly as per if it was
	// operating on a value produced by `TargetType.New()`.
	UnmarshalTransformFunc UnmarshalTransformFunc
	// The type of value we will manufacture an instance of and unmarshal
	// into, then when done provide to the UnmarshalTransformFunc.
	//
	// The match between transform func and target type should be checked
	// during construction of this AtlasEntry.
	UnmarshalTransformTargetType reflect.Type

	// --------------------------------------------------------
	// Standard options for how to map (varies by Kind)
	// --------------------------------------------------------

	// A "tag" to emit when marshalling this type of value;
	// and when unmarshalling, this tag will cause unmarshal to pick
	// this atlas (and if there's conflicting type info, error).
	Tag int
	// Flag for whether the Tag feature should be used (zero is a valid tag).
	Tagged bool

	// A mapping of fields in a struct to serial keys.
	// Only valid if `this.Type.Kind() == Struct`.
	StructMap *StructMap

	// Configuration for how to traverse a map kind.
	// Only valid if `this.Type.Kind() == Map`.
	MapMorphism *MapMorphism

	// Configuration for how to pick concrete types to fill a union interface.
	// Only valid if `this.Type.Kind() == Interface`.
	UnionKeyedMorphism *UnionKeyedMorphism

	// FUTURE: enum-ish primitives, multiplexers for interfaces,
	//  lots of such things will belong here.

	// --------------------------------------------------------
	// Hooks, validate helpers
	// --------------------------------------------------------

	// A validation function which will be called for the whole value
	// after unmarshalling reached the end of the object.
	// If it returns an error, the entire unmarshal will error.
	//
	// Not used in marshalling.
	// Not reachable if an UnmarshalTransform is set.
	ValidateFn func(v interface{}) error
}

func BuildEntry(typeHintObj interface{}) *BuilderCore {
	rt := reflect.TypeOf(typeHintObj)
	if rt.Kind() == reflect.Ptr {
		if rt.Elem().Kind() == reflect.Interface {
			rt = rt.Elem()
		} else {
			panic("invalid atlas build: use the bare object, not a pointer (refmt will handle pointers automatically)")
		}
	}
	return &BuilderCore{
		&AtlasEntry{Type: rt},
	}
}

/*
	Intermediate step in building an AtlasEntry: use `BuildEntry` to
	get one of these to start with, then call one of the methods
	on this type to get a specialized builder which has the methods
	relevant for setting up that specific kind of mapping.

	One full example of using this builder may look like the following:

		atlas.BuildEntry(Formula{}).StructMap().Autogenerate().Complete()

	Some intermediate manipulations may be performed on this object,
	for example setting the "tag" (if you want to use cbor tagging),
	before calling the specializer method.
	In this case, just keep chaining the configuration calls like so:

		atlas.BuildEntry(Formula{}).UseTag(4000)
			.StructMap().Autogenerate().Complete()

*/
type BuilderCore struct {
	entry *AtlasEntry
}

func (x *BuilderCore) UseTag(tag int) *BuilderCore {
	x.entry.Tagged = true
	x.entry.Tag = tag
	return x
}
