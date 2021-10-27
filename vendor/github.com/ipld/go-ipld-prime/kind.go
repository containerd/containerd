package ipld

// Kind represents the primitive kind in the IPLD data model.
// All of these kinds map directly onto serializable data.
//
// Note that Kind contains the concept of "map", but not "struct"
// or "object" -- those are a concepts that could be introduced in a
// type system layers, but are *not* present in the data model layer,
// and therefore they aren't included in the Kind enum.
type Kind uint8

const (
	Kind_Invalid Kind = 0
	Kind_Map     Kind = '{'
	Kind_List    Kind = '['
	Kind_Null    Kind = '0'
	Kind_Bool    Kind = 'b'
	Kind_Int     Kind = 'i'
	Kind_Float   Kind = 'f'
	Kind_String  Kind = 's'
	Kind_Bytes   Kind = 'x'
	Kind_Link    Kind = '/'
)

func (k Kind) String() string {
	switch k {
	case Kind_Invalid:
		return "INVALID"
	case Kind_Map:
		return "map"
	case Kind_List:
		return "list"
	case Kind_Null:
		return "null"
	case Kind_Bool:
		return "bool"
	case Kind_Int:
		return "int"
	case Kind_Float:
		return "float"
	case Kind_String:
		return "string"
	case Kind_Bytes:
		return "bytes"
	case Kind_Link:
		return "link"
	default:
		panic("invalid enumeration value!")
	}
}

// KindSet is a type with a few enumerated consts that are commonly used
// (mostly, in error messages).
type KindSet []Kind

var (
	KindSet_Recursive = KindSet{Kind_Map, Kind_List}
	KindSet_Scalar    = KindSet{Kind_Null, Kind_Bool, Kind_Int, Kind_Float, Kind_String, Kind_Bytes, Kind_Link}

	KindSet_JustMap    = KindSet{Kind_Map}
	KindSet_JustList   = KindSet{Kind_List}
	KindSet_JustNull   = KindSet{Kind_Null}
	KindSet_JustBool   = KindSet{Kind_Bool}
	KindSet_JustInt    = KindSet{Kind_Int}
	KindSet_JustFloat  = KindSet{Kind_Float}
	KindSet_JustString = KindSet{Kind_String}
	KindSet_JustBytes  = KindSet{Kind_Bytes}
	KindSet_JustLink   = KindSet{Kind_Link}
)

func (x KindSet) String() string {
	s := ""
	for i := 0; i < len(x)-1; i++ {
		s += x[i].String() + " or "
	}
	s += x[len(x)-1].String()
	return s
}

func (x KindSet) Contains(e Kind) bool {
	for _, v := range x {
		if v == e {
			return true
		}
	}
	return false
}
