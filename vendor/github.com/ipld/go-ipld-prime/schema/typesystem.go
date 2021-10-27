package schema

type TypeSystem struct {
	// namedTypes is the set of all named types in this universe.
	// The map's key is the value's Name() property and must be unique.
	//
	// The IsAnonymous property is false for all values in this map that
	// support the IsAnonymous property.
	//
	// Each Type in the universe may only refer to other types in their
	// definition if those type are either A) in this namedTypes map,
	// or B) are IsAnonymous==true.
	namedTypes map[TypeName]Type
}
