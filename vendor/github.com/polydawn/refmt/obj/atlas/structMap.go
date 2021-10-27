package atlas

import "reflect"

type StructMap struct {
	// A slice of descriptions of each field in the type.
	// Each entry specifies the name by which each field should be referenced
	// when serialized, and defines a way to get an address to the field.
	Fields []StructMapEntry
}

type StructMapEntry struct {
	// The field name; will be emitted as token during marshal, and used for
	// lookup during unmarshal.  Required.
	SerialName string

	// If true, a key token with this SerialName will be ignored during unmarshal.
	// (By default, if there's no StructMapEntry for a key token, it's an error.)
	// If true, the ReflectRoute, Type, etc fields are irrelevant and may be nil.
	Ignore bool

	ReflectRoute ReflectRoute // reflection generates these.
	Type         reflect.Type // type to expect on the far side of the ReflectRoute.
	tagged       bool         // used during autogen.

	// Theoretical feature which would be alternative to ReflectRoute.  Support dropped for the moment.
	//addrFunc     func(interface{}) interface{} // custom user function.

	// If true, marshalling will skip this field if it's the zero value.
	OmitEmpty bool
}

type ReflectRoute []int

func (rr ReflectRoute) TraverseToValue(v reflect.Value) reflect.Value {
	for _, i := range rr {
		if v.Kind() == reflect.Ptr {
			if v.IsNil() {
				return reflect.Value{}
			}
			v = v.Elem()
		}
		v = v.Field(i)
	}
	return v
}
