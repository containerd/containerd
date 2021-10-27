package ipld

import (
	"fmt"
	"strings"
)

// ErrWrongKind may be returned from functions on the Node interface when
// a method is invoked which doesn't make sense for the Kind that node
// concretely contains.
//
// For example, calling AsString on a map will return ErrWrongKind.
// Calling Lookup on an int will similarly return ErrWrongKind.
type ErrWrongKind struct {
	// TypeName may optionally indicate the named type of a node the function
	// was called on (if the node was typed!), or, may be the empty string.
	TypeName string

	// MethodName is literally the string for the operation attempted, e.g.
	// "AsString".
	//
	// For methods on nodebuilders, we say e.g. "NodeBuilder.CreateMap".
	MethodName string

	// ApprorpriateKind describes which Kinds the erroring method would
	// make sense for.
	AppropriateKind KindSet

	// ActualKind describes the Kind of the node the method was called on.
	//
	// In the case of typed nodes, this will typically refer to the 'natural'
	// data-model kind for such a type (e.g., structs will say 'map' here).
	ActualKind Kind
}

func (e ErrWrongKind) Error() string {
	if e.TypeName == "" {
		return fmt.Sprintf("func called on wrong kind: %s called on a %s node, but only makes sense on %s", e.MethodName, e.ActualKind, e.AppropriateKind)
	} else {
		return fmt.Sprintf("func called on wrong kind: %s called on a %s node (kind: %s), but only makes sense on %s", e.MethodName, e.TypeName, e.ActualKind, e.AppropriateKind)
	}
}

// ErrNotExists may be returned from the lookup functions of the Node interface
// to indicate a missing value.
//
// Note that schema.ErrNoSuchField is another type of error which sometimes
// occurs in similar places as ErrNotExists.  ErrNoSuchField is preferred
// when handling data with constraints provided by a schema that mean that
// a field can *never* exist (as differentiated from a map key which is
// simply absent in some data).
type ErrNotExists struct {
	Segment PathSegment
}

func (e ErrNotExists) Error() string {
	return fmt.Sprintf("key not found: %q", e.Segment)
}

// ErrRepeatedMapKey is an error indicating that a key was inserted
// into a map that already contains that key.
//
// This error may be returned by any methods that add data to a map --
// any of the methods on a NodeAssembler that was yielded by MapAssembler.AssignKey(),
// or from the MapAssembler.AssignDirectly() method.
type ErrRepeatedMapKey struct {
	Key Node
}

func (e ErrRepeatedMapKey) Error() string {
	return fmt.Sprintf("cannot repeat map key %q", e.Key)
}

// ErrInvalidKey indicates a key is invalid for some reason.
//
// This is only possible for typed nodes; specifically, it may show up when
// handling struct types, or maps with interesting key types.
// (Other kinds of key invalidity that happen for untyped maps
// fall under ErrRepeatedMapKey or ErrWrongKind.)
// (Union types use ErrInvalidUnionDiscriminant instead of ErrInvalidKey,
// even when their representation strategy is maplike.)
type ErrInvalidKey struct {
	// TypeName will indicate the named type of a node the function was called on.
	TypeName string

	// Key is the key that was rejected.
	Key Node

	// Reason, if set, may provide details (for example, the reason a key couldn't be converted to a type).
	// If absent, it'll be presumed "no such field".
	// ErrUnmatchable may show up as a reason for typed maps with complex keys.
	Reason error
}

func (e ErrInvalidKey) Error() string {
	if e.Reason == nil {
		return fmt.Sprintf("invalid key for map %s: %q: no such field", e.TypeName, e.Key)
	} else {
		return fmt.Sprintf("invalid key for map %s: %q: %s", e.TypeName, e.Key, e.Reason)
	}
}

// ErrInvalidSegmentForList is returned when using Node.LookupBySegment and the
// given PathSegment can't be applied to a list because it's unparsable as a number.
type ErrInvalidSegmentForList struct {
	// TypeName may indicate the named type of a node the function was called on,
	// or be empty string if working on untyped data.
	TypeName string

	// TroubleSegment is the segment we couldn't use.
	TroubleSegment PathSegment

	// Reason may explain more about why the PathSegment couldn't be used;
	// in practice, it's probably a 'strconv.NumError'.
	Reason error
}

func (e ErrInvalidSegmentForList) Error() string {
	v := "invalid segment for lookup on a list"
	if e.TypeName != "" {
		v += " of type " + e.TypeName
	}
	return v + fmt.Sprintf(": %q: %s", e.TroubleSegment.s, e.Reason)
}

// ErrHashMismatch is the error returned when loading data and verifying its hash
// and finding that the loaded data doesn't re-hash to the expected value.
// It is typically seen returned by functions like LinkSystem.Load or LinkSystem.Fill.
type ErrHashMismatch struct {
	Actual   Link
	Expected Link
}

func (e ErrHashMismatch) Error() string {
	return fmt.Sprintf("hash mismatch!  %v (actual) != %v (expected)", e.Actual, e.Expected)
}

// ErrUnmatchable is the error raised when processing data with IPLD Schemas and
// finding data which cannot be matched into the schema.
// It will be returned by NodeAssemblers and NodeBuilders when they are fed unmatchable data.
// As a result, it will also often be seen returned from unmarshalling
// when unmarshalling into schema-constrained NodeAssemblers.
//
// ErrUnmatchable provides the name of the type in the schema that data couldn't be matched to,
// and wraps another error as the more detailed reason.
type ErrUnmatchable struct {
	// TypeName will indicate the named type of a node the function was called on.
	TypeName string

	// Reason must always be present.  ErrUnmatchable doesn't say much otherwise.
	Reason error
}

func (e ErrUnmatchable) Error() string {
	return fmt.Sprintf("matching data to schema of %s rejected: %s", e.TypeName, e.Reason)
}

// Reasonf returns a new ErrUnmatchable with a Reason field set to the Errorf of the arguments.
// It's a helper function for creating untyped error reasons without importing the fmt package.
func (e ErrUnmatchable) Reasonf(format string, a ...interface{}) ErrUnmatchable {
	return ErrUnmatchable{e.TypeName, fmt.Errorf(format, a...)}
}

// ErrIteratorOverread is returned when calling 'Next' on a MapIterator or
// ListIterator when it is already done.
type ErrIteratorOverread struct{}

func (e ErrIteratorOverread) Error() string {
	return "iterator overread"
}

type ErrCannotBeNull struct{} // Review: arguably either ErrInvalidKindForNodePrototype.

// ErrMissingRequiredField is returned when calling 'Finish' on a NodeAssembler
// for a Struct that has not has all required fields set.
type ErrMissingRequiredField struct {
	Missing []string
}

func (e ErrMissingRequiredField) Error() string {
	return "missing required fields: " + strings.Join(e.Missing, ",")
}

type ErrListOverrun struct{}              // only possible for typed nodes -- specifically, struct types with list (aka tuple) representations.
type ErrInvalidUnionDiscriminant struct{} // only possible for typed nodes -- specifically, union types.
