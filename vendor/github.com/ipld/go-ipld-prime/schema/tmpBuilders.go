package schema

import (
	"fmt"

	"github.com/ipld/go-ipld-prime"
)

// Everything in this file is __a temporary hack__ and will be __removed__.
//
// These methods will only hang around until more of the "ast" packages are finished;
// thereafter, building schema.Type and schema.TypeSystem values will only be
// possible through first constructing a schema AST, and *then* using Reify(),
// which will validate things correctly, cycle-check, cross-link, etc.
//
// (Meanwhile, we're using these methods in the codegen prototypes.)

// These methods use Type objects as parameters when pointing to other things,
//  but this is... turning out consistently problematic.
//   Even when we're doing this hacky direct-call doesn't-need-to-be-serializable temp stuff,
//    as written, this doesn't actually let us express cyclic things viably!
//   The same initialization questions are also going to come up again when we try to make
//    concrete values in the output of codegen.
// Maybe it's actually just a bad idea to have our reified Type types use Type pointers at all.
//  (I will never get tired of the tongue twisters, evidently.)
//  I'm not actually using that much, and it's always avoidable (it's trivial to replace with a map lookup bouncing through a 'ts' variable somewhere).
//  And having the AST gen'd types be... just... the thing... sounds nice.  It could save a lot of work.
//   (It would mean the golang types don't tell you whether the values have been checked for global properties or not, but, eh.)
//   (It's not really compatible with "Prototype and Type are the same thing for codegen'd stuff", either (or, we need more interfaces, and to *really* lean into them), but maybe that's okay.)

func SpawnString(name TypeName) *TypeString {
	return &TypeString{typeBase{name, nil}}
}

func SpawnBool(name TypeName) *TypeBool {
	return &TypeBool{typeBase{name, nil}}
}

func SpawnInt(name TypeName) *TypeInt {
	return &TypeInt{typeBase{name, nil}}
}

func SpawnFloat(name TypeName) *TypeFloat {
	return &TypeFloat{typeBase{name, nil}}
}

func SpawnBytes(name TypeName) *TypeBytes {
	return &TypeBytes{typeBase{name, nil}}
}

func SpawnLink(name TypeName) *TypeLink {
	return &TypeLink{typeBase{name, nil}, "", false}
}

func SpawnLinkReference(name TypeName, pointsTo TypeName) *TypeLink {
	return &TypeLink{typeBase{name, nil}, pointsTo, true}
}

func SpawnList(name TypeName, valueType TypeName, nullable bool) *TypeList {
	return &TypeList{typeBase{name, nil}, false, valueType, nullable}
}

func SpawnMap(name TypeName, keyType TypeName, valueType TypeName, nullable bool) *TypeMap {
	return &TypeMap{typeBase{name, nil}, false, keyType, valueType, nullable}
}

func SpawnStruct(name TypeName, fields []StructField, repr StructRepresentation) *TypeStruct {
	v := &TypeStruct{
		typeBase{name, nil},
		fields,
		make(map[string]StructField, len(fields)),
		repr,
	}
	for i := range fields {
		fields[i].parent = v
		v.fieldsMap[fields[i].name] = fields[i]
	}
	switch repr.(type) {
	case StructRepresentation_Stringjoin:
		for _, f := range fields {
			if f.IsMaybe() {
				panic("neither nullable nor optional is supported on struct stringjoin representation")
			}
		}
	}
	return v
}
func SpawnStructField(name string, typ TypeName, optional bool, nullable bool) StructField {
	return StructField{nil /*populated later*/, name, typ, optional, nullable}
}
func SpawnStructRepresentationMap(renames map[string]string) StructRepresentation_Map {
	return StructRepresentation_Map{renames, nil}
}
func SpawnStructRepresentationTuple() StructRepresentation_Tuple {
	return StructRepresentation_Tuple{}
}
func SpawnStructRepresentationStringjoin(delim string) StructRepresentation_Stringjoin {
	return StructRepresentation_Stringjoin{delim}
}

func SpawnUnion(name TypeName, members []TypeName, repr UnionRepresentation) *TypeUnion {
	return &TypeUnion{typeBase{name, nil}, members, repr}
}
func SpawnUnionRepresentationKeyed(table map[string]TypeName) UnionRepresentation_Keyed {
	return UnionRepresentation_Keyed{table}
}
func SpawnUnionRepresentationKinded(table map[ipld.Kind]TypeName) UnionRepresentation_Kinded {
	return UnionRepresentation_Kinded{table}
}
func SpawnUnionRepresentationStringprefix(delim string, table map[string]TypeName) UnionRepresentation_Stringprefix {
	return UnionRepresentation_Stringprefix{delim, table}
}

// The methods relating to TypeSystem are also mutation-heavy and placeholdery.

func (ts *TypeSystem) Init() {
	ts.namedTypes = make(map[TypeName]Type)
}
func (ts *TypeSystem) Accumulate(typ Type) {
	typ._Type(ts)
	ts.namedTypes[typ.Name()] = typ
}
func (ts TypeSystem) GetTypes() map[TypeName]Type {
	return ts.namedTypes
}
func (ts TypeSystem) TypeByName(n string) Type {
	return ts.namedTypes[TypeName(n)]
}

// ValidateGraph checks that all type names referenced are defined.
//
// It does not do any other validations of individual type's sensibleness
// (that should've happened when they were created
// (although also note many of those validates are NYI,
// and are roadmapped for after we research self-hosting)).
func (ts TypeSystem) ValidateGraph() []error {
	var ee []error
	for tn, t := range ts.namedTypes {
		switch t2 := t.(type) {
		case *TypeBool,
			*TypeInt,
			*TypeFloat,
			*TypeString,
			*TypeBytes,
			*TypeEnum:
			continue // nothing to check: these are leaf nodes and refer to no other types.
		case *TypeLink:
			if !t2.hasReferencedType {
				continue
			}
			if _, ok := ts.namedTypes[t2.referencedType]; !ok {
				ee = append(ee, fmt.Errorf("type %s refers to missing type %s (as link reference type)", tn, t2.referencedType))
			}
		case *TypeStruct:
			for _, f := range t2.fields {
				if _, ok := ts.namedTypes[f.typ]; !ok {
					ee = append(ee, fmt.Errorf("type %s refers to missing type %s (in field %s)", tn, f.typ, f.name))
				}
			}
		case *TypeMap:
			if _, ok := ts.namedTypes[t2.keyType]; !ok {
				ee = append(ee, fmt.Errorf("type %s refers to missing type %s (as key type)", tn, t2.keyType))
			}
			if _, ok := ts.namedTypes[t2.valueType]; !ok {
				ee = append(ee, fmt.Errorf("type %s refers to missing type %s (as key type)", tn, t2.valueType))
			}
		case *TypeList:
			if _, ok := ts.namedTypes[t2.valueType]; !ok {
				ee = append(ee, fmt.Errorf("type %s refers to missing type %s (as key type)", tn, t2.valueType))
			}
		case *TypeUnion:
			for _, mn := range t2.members {
				if _, ok := ts.namedTypes[mn]; !ok {
					ee = append(ee, fmt.Errorf("type %s refers to missing type %s (as a member)", tn, mn))
				}
			}
		}
	}
	return ee
}
