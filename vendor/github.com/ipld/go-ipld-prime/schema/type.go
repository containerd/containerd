package schema

import (
	ipld "github.com/ipld/go-ipld-prime"
)

type TypeName string // = ast.TypeName

func (tn TypeName) String() string { return string(tn) }

// typesystem.Type is an union interface; each of the `Type*` concrete types
// in this package are one of its members.
//
// Specifically,
//
// 	TypeBool
// 	TypeString
// 	TypeBytes
// 	TypeInt
// 	TypeFloat
// 	TypeMap
// 	TypeList
// 	TypeLink
// 	TypeUnion
// 	TypeStruct
// 	TypeEnum
//
// are all of the kinds of Type.
//
// This is a closed union; you can switch upon the above members without
// including a default case.  The membership is closed by the unexported
// '_Type' method; you may use the BurntSushi/go-sumtype tool to check
// your switches for completeness.
//
// Many interesting properties of each Type are only defined for that specific
// type, so it's typical to use a type switch to handle each type of Type.
// (Your humble author is truly sorry for the word-mash that results from
// attempting to describe the types that describe the typesystem.Type.)
//
// For example, to inspect the kind of fields in a struct: you might
// cast a `Type` interface into `TypeStruct`, and then the `Fields()` on
// that `TypeStruct` can be inspected.  (`Fields()` isn't defined for any
// other kind of Type.)
type Type interface {
	// Unexported marker method to force the union closed.
	// Also used to set the internal pointer back to the universe its part of.
	_Type(*TypeSystem)

	// Returns a pointer to the TypeSystem this Type is a member of.
	TypeSystem() *TypeSystem

	// Returns the string name of the Type.  This name is unique within the
	// universe this type is a member of, *unless* this type is Anonymous,
	// in which case a string describing the type will still be returned, but
	// that string will not be required to be unique.
	Name() TypeName

	// Returns the TypeKind of this Type.
	//
	// The returned value is a 1:1 association with which of the concrete
	// "schema.Type*" structs this interface can be cast to.
	//
	// Note that a schema.TypeKind is a different enum than ipld.Kind;
	// and furthermore, there's no strict relationship between them.
	// schema.TypedNode values can be described by *two* distinct Kinds:
	// one which describes how the Node itself will act,
	// and another which describes how the Node presents for serialization.
	// For some combinations of Type and representation strategy, one or both
	// of the Kinds can be determined statically; but not always:
	// it can sometimes be necessary to inspect the value quite concretely
	// (e.g., `schema.TypedNode{}.Representation().Kind()`) in order to find
	// out exactly how a node will be serialized!  This is because some types
	// can vary in representation kind based on their value (specifically,
	// kinded-representation unions have this property).
	TypeKind() TypeKind

	// RepresentationBehavior returns a description of how the representation
	// of this type will behave in terms of the IPLD Data Model.
	// This property varies based on the representation strategy of a type.
	//
	// In one case, the representation behavior cannot be known statically,
	// and varies based on the data: kinded unions have this trait.
	//
	// This property is used by kinded unions, which require that their members
	// all have distinct representation behavior.
	// (It follows that a kinded union cannot have another kinded union as a member.)
	//
	// You may also be interested in a related property that might have been called "TypeBehavior".
	// However, this method doesn't exist, because it's a deterministic property of `TypeKind()`!
	// You can use `TypeKind.ActsLike()` to get type-level behavioral information.
	RepresentationBehavior() ipld.Kind
}

var (
	_ Type = &TypeBool{}
	_ Type = &TypeString{}
	_ Type = &TypeBytes{}
	_ Type = &TypeInt{}
	_ Type = &TypeFloat{}
	_ Type = &TypeMap{}
	_ Type = &TypeList{}
	_ Type = &TypeLink{}
	_ Type = &TypeUnion{}
	_ Type = &TypeStruct{}
	_ Type = &TypeEnum{}
)

type typeBase struct {
	name     TypeName
	universe *TypeSystem
}

type TypeBool struct {
	typeBase
}

type TypeString struct {
	typeBase
}

type TypeBytes struct {
	typeBase
}

type TypeInt struct {
	typeBase
}

type TypeFloat struct {
	typeBase
}

type TypeMap struct {
	typeBase
	anonymous     bool
	keyType       TypeName // must be Kind==string (e.g. Type==String|Enum).
	valueType     TypeName
	valueNullable bool
}

type TypeList struct {
	typeBase
	anonymous     bool
	valueType     TypeName
	valueNullable bool
}

type TypeLink struct {
	typeBase
	referencedType    TypeName
	hasReferencedType bool
	// ...?
}

type TypeUnion struct {
	typeBase
	// Members are listed in the order they appear in the schema.
	// To find the discriminant info, you must look inside the representation; they all contain a 'table' of some kind in which the member types are the values.
	// Note that multiple appearances of the same type as distinct members of the union is not possible.
	//  While we could do this... A: that's... odd, and nearly never called for; B: not possible with kinded mode; C: imagine the golang-native type switch!  it's impossible.
	//  We rely on this clarity in many ways: most visibly, the type-level Node implementation for a union always uses the type names as if they were map keys!  This behavior is consistent for all union representations.
	members        []TypeName
	representation UnionRepresentation
}

type UnionRepresentation interface{ _UnionRepresentation() }

func (UnionRepresentation_Keyed) _UnionRepresentation()        {}
func (UnionRepresentation_Kinded) _UnionRepresentation()       {}
func (UnionRepresentation_Envelope) _UnionRepresentation()     {}
func (UnionRepresentation_Inline) _UnionRepresentation()       {}
func (UnionRepresentation_Stringprefix) _UnionRepresentation() {}

// A bunch of these tables in union representation might be easier to use if flipped;
//  we almost always index into them by type (since that's what we have an ordered list of);
//  and they're unique in both directions, so it's equally valid either way.
//  The order they're currently written in matches the serial form in the schema AST.

type UnionRepresentation_Keyed struct {
	table map[string]TypeName // key is user-defined freetext
}
type UnionRepresentation_Kinded struct {
	table map[ipld.Kind]TypeName
}
type UnionRepresentation_Envelope struct {
	discriminantKey string
	contentKey      string
	table           map[string]TypeName // key is user-defined freetext
}
type UnionRepresentation_Inline struct {
	discriminantKey string
	table           map[string]TypeName // key is user-defined freetext
}
type UnionRepresentation_Stringprefix struct {
	delim string
	table map[string]TypeName // key is user-defined freetext
}

type TypeStruct struct {
	typeBase
	// n.b. `Fields` is an (order-preserving!) map in the schema-schema;
	//  but it's a list here, with the keys denormalized into the value,
	//   because that's typically how we use it.
	fields         []StructField
	fieldsMap      map[string]StructField // same content, indexed for lookup.
	representation StructRepresentation
}
type StructField struct {
	parent   *TypeStruct
	name     string
	typ      TypeName
	optional bool
	nullable bool
}

type StructRepresentation interface{ _StructRepresentation() }

func (StructRepresentation_Map) _StructRepresentation()         {}
func (StructRepresentation_Tuple) _StructRepresentation()       {}
func (StructRepresentation_StringPairs) _StructRepresentation() {}
func (StructRepresentation_Stringjoin) _StructRepresentation()  {}

type StructRepresentation_Map struct {
	renames   map[string]string
	implicits map[string]ImplicitValue
}
type StructRepresentation_Tuple struct{}
type StructRepresentation_StringPairs struct{ sep1, sep2 string }
type StructRepresentation_Stringjoin struct{ sep string }

type TypeEnum struct {
	typeBase
	members []string
}

// ImplicitValue is an sum type holding values that are implicits.
// It's not an 'Any' value because it can't be recursive
// (or to be slightly more specific, it can be one of the recursive kinds,
// but if so, only its empty value is valid here).
type ImplicitValue interface{ _ImplicitValue() }

type ImplicitValue_EmptyList struct{}
type ImplicitValue_EmptyMap struct{}
type ImplicitValue_String struct{ x string }
type ImplicitValue_Int struct{ x int }
