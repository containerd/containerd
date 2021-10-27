package schema

import (
	ipld "github.com/ipld/go-ipld-prime"
)

/* cookie-cutter standard interface stuff */

func (t *typeBase) _Type(ts *TypeSystem) {
	t.universe = ts
}
func (t typeBase) TypeSystem() *TypeSystem { return t.universe }
func (t typeBase) Name() TypeName          { return t.name }

func (TypeBool) TypeKind() TypeKind   { return TypeKind_Bool }
func (TypeString) TypeKind() TypeKind { return TypeKind_String }
func (TypeBytes) TypeKind() TypeKind  { return TypeKind_Bytes }
func (TypeInt) TypeKind() TypeKind    { return TypeKind_Int }
func (TypeFloat) TypeKind() TypeKind  { return TypeKind_Float }
func (TypeMap) TypeKind() TypeKind    { return TypeKind_Map }
func (TypeList) TypeKind() TypeKind   { return TypeKind_List }
func (TypeLink) TypeKind() TypeKind   { return TypeKind_Link }
func (TypeUnion) TypeKind() TypeKind  { return TypeKind_Union }
func (TypeStruct) TypeKind() TypeKind { return TypeKind_Struct }
func (TypeEnum) TypeKind() TypeKind   { return TypeKind_Enum }

func (TypeBool) RepresentationBehavior() ipld.Kind   { return ipld.Kind_Bool }
func (TypeString) RepresentationBehavior() ipld.Kind { return ipld.Kind_String }
func (TypeBytes) RepresentationBehavior() ipld.Kind  { return ipld.Kind_Bytes }
func (TypeInt) RepresentationBehavior() ipld.Kind    { return ipld.Kind_Int }
func (TypeFloat) RepresentationBehavior() ipld.Kind  { return ipld.Kind_Float }
func (TypeMap) RepresentationBehavior() ipld.Kind    { return ipld.Kind_Map }
func (TypeList) RepresentationBehavior() ipld.Kind   { return ipld.Kind_List }
func (TypeLink) RepresentationBehavior() ipld.Kind   { return ipld.Kind_Link }
func (t TypeUnion) RepresentationBehavior() ipld.Kind {
	switch t.representation.(type) {
	case UnionRepresentation_Keyed:
		return ipld.Kind_Map
	case UnionRepresentation_Kinded:
		return ipld.Kind_Invalid // you can't know with this one, until you see the value (and thus can its inhabitant's behavior)!
	case UnionRepresentation_Envelope:
		return ipld.Kind_Map
	case UnionRepresentation_Inline:
		return ipld.Kind_Map
	default:
		panic("unreachable")
	}
}
func (t TypeStruct) RepresentationBehavior() ipld.Kind {
	switch t.representation.(type) {
	case StructRepresentation_Map:
		return ipld.Kind_Map
	case StructRepresentation_Tuple:
		return ipld.Kind_List
	case StructRepresentation_StringPairs:
		return ipld.Kind_String
	case StructRepresentation_Stringjoin:
		return ipld.Kind_String
	default:
		panic("unreachable")
	}
}
func (t TypeEnum) RepresentationBehavior() ipld.Kind {
	// TODO: this should have a representation strategy switch too; sometimes that will indicate int representation behavior.
	return ipld.Kind_String
}

/* interesting methods per Type type */

// beware: many of these methods will change when we successfully bootstrap self-hosting.
//
// The current methods return reified Type objects; in the future, there might be less of that.
// Returning reified Type objects requires bouncing lookups through the typesystem map;
// this is unavoidable because we need to handle cycles in definitions.
// However, the extra (and cyclic) pointers that requires won't necessarily jive well if
// we remake the Type types to have close resemblances to the Data Model tree data.)
//
// It's also unfortunate that some of the current methods collide in name with
// the names of the Data Model fields.  We might reshuffling things to reduce this.
//
// At any rate, all of these changes will come as a sweep once we
// get a self-hosting gen of the schema-schema, not before
// (the effort of updating template references is substantial).

// IsAnonymous is returns true if the type was unnamed.  Unnamed types will
// claim to have a Name property like `{Foo:Bar}`, and this is not guaranteed
// to be a unique string for all types in the universe.
func (t TypeMap) IsAnonymous() bool {
	return t.anonymous
}

// KeyType returns the Type of the map keys.
//
// Note that map keys will must always be some type which is representable as a
// string in the IPLD Data Model (e.g. either TypeString or TypeEnum).
func (t TypeMap) KeyType() Type {
	return t.universe.namedTypes[t.keyType]
}

// ValueType returns the Type of the map values.
func (t TypeMap) ValueType() Type {
	return t.universe.namedTypes[t.valueType]
}

// ValueIsNullable returns a bool describing if the map values are permitted
// to be null.
func (t TypeMap) ValueIsNullable() bool {
	return t.valueNullable
}

// IsAnonymous is returns true if the type was unnamed.  Unnamed types will
// claim to have a Name property like `[Foo]`, and this is not guaranteed
// to be a unique string for all types in the universe.
func (t TypeList) IsAnonymous() bool {
	return t.anonymous
}

// ValueType returns to the Type of the list values.
func (t TypeList) ValueType() Type {
	return t.universe.namedTypes[t.valueType]
}

// ValueIsNullable returns a bool describing if the list values are permitted
// to be null.
func (t TypeList) ValueIsNullable() bool {
	return t.valueNullable
}

// Members returns the list of all types that are possible inhabitants of this union.
func (t TypeUnion) Members() []Type {
	a := make([]Type, len(t.members))
	for i := range t.members {
		a[i] = t.universe.namedTypes[t.members[i]]
	}
	return a
}

func (t TypeUnion) RepresentationStrategy() UnionRepresentation {
	return t.representation
}

func (r UnionRepresentation_Keyed) GetDiscriminant(t Type) string {
	for d, t2 := range r.table {
		if t2 == t.Name() {
			return d
		}
	}
	panic("that type isn't a member of this union")
}

func (r UnionRepresentation_Stringprefix) GetDelim() string {
	return r.delim
}

func (r UnionRepresentation_Stringprefix) GetDiscriminant(t Type) string {
	for d, t2 := range r.table {
		if t2 == t.Name() {
			return d
		}
	}
	panic("that type isn't a member of this union")
}

// GetMember returns type info for the member matching the kind argument,
// or may return nil if that kind is not mapped to a member of this union.
func (r UnionRepresentation_Kinded) GetMember(k ipld.Kind) TypeName {
	return r.table[k]
}

// Fields returns a slice of descriptions of the object's fields.
func (t TypeStruct) Fields() []StructField {
	a := make([]StructField, len(t.fields))
	for i := range t.fields {
		a[i] = t.fields[i]
	}
	return a
}

// Field looks up a StructField by name, or returns nil if no such field.
func (t TypeStruct) Field(name string) *StructField {
	if v, ok := t.fieldsMap[name]; ok {
		return &v
	}
	return nil
}

// Parent returns the type information that this field describes a part of.
//
// While in many cases, you may know the parent already from context,
// there may still be situations where want to pass around a field and
// not need to continue passing down the parent type with it; this method
// helps your code be less redundant in such a situation.
// (You'll find this useful for looking up any rename directives, for example,
// when holding onto a field, since that requires looking up information from
// the representation strategy, which is a property of the type as a whole.)
func (f StructField) Parent() *TypeStruct { return f.parent }

// Name returns the string name of this field.  The name is the string that
// will be used as a map key if the structure this field is a member of is
// serialized as a map representation.
func (f StructField) Name() string { return f.name }

// Type returns the Type of this field's value.  Note the field may
// also be unset if it is either Optional or Nullable.
func (f StructField) Type() Type { return f.parent.universe.namedTypes[f.typ] }

// IsOptional returns true if the field is allowed to be absent from the object.
// If IsOptional is false, the field may be absent from the serial representation
// of the object entirely.
//
// Note being optional is different than saying the value is permitted to be null!
// A field may be both nullable and optional simultaneously, or either, or neither.
func (f StructField) IsOptional() bool { return f.optional }

// IsNullable returns true if the field value is allowed to be null.
//
// If is Nullable is false, note that it's still possible that the field value
// will be absent if the field is Optional!  Being nullable is unrelated to
// whether the field's presence is optional as a whole.
//
// Note that a field may be both nullable and optional simultaneously,
// or either, or neither.
func (f StructField) IsNullable() bool { return f.nullable }

// IsMaybe returns true if the field value is allowed to be either null or absent.
//
// This is a simple "or" of the two properties,
// but this method is a shorthand that turns out useful often.
func (f StructField) IsMaybe() bool { return f.nullable || f.optional }

func (t TypeStruct) RepresentationStrategy() StructRepresentation {
	return t.representation
}

func (r StructRepresentation_Map) GetFieldKey(field StructField) string {
	if n, ok := r.renames[field.name]; ok {
		return n
	}
	return field.name
}

func (r StructRepresentation_Map) FieldHasRename(field StructField) bool {
	_, ok := r.renames[field.name]
	return ok
}

func (r StructRepresentation_Stringjoin) GetDelim() string {
	return r.sep
}

// Members returns a slice the strings which are valid inhabitants of this enum.
func (t TypeEnum) Members() []string {
	a := make([]string, len(t.members))
	for i := range t.members {
		a[i] = t.members[i]
	}
	return a
}

// Links can keep a referenced type, which is a hint only about the data on the
// other side of the link, no something that can be explicitly validated without
// loading the link

// HasReferencedType returns true if the link has a hint about the type it references
// false if it's generic
func (t TypeLink) HasReferencedType() bool {
	return t.hasReferencedType
}

// ReferencedType returns the type hint for the node on the other side of the link
func (t TypeLink) ReferencedType() Type {
	return t.universe.namedTypes[t.referencedType]
}
