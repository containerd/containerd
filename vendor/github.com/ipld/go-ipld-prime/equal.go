package ipld

// DeepEqual reports whether x and y are "deeply equal" as IPLD nodes.
// This is similar to reflect.DeepEqual, but based around the Node interface.
//
// Two nodes must have the same kind to be deeply equal.
// If either node has the invalid kind, the nodes are not deeply equal.
//
// Two nodes of scalar kinds (null, bool, int, float, string, bytes, link)
// are deeply equal if their Go values, as returned by AsKind methods, are equal as
// per Go's == comparison operator.
//
// Note that Links are compared in a shallow way, without being followed.
// This will generally be enough, as it's rare to have two different links to the
// same IPLD data by using a different codec or multihash type.
//
// Two nodes of recursive kinds (map, list)
// must have the same length to be deeply equal.
// Their elements, as reported by iterators, must be deeply equal.
// The elements are compared in the iterator's order,
// meaning two maps sorting the same keys differently might not be equal.
//
// Note that this function panics if either Node returns an error.
// We only call valid methods for each Kind,
// so an error should only happen if a Node implementation breaks that contract.
// It is generally not recommended to call DeepEqual on ADL nodes.
func DeepEqual(x, y Node) bool {
	xk, yk := x.Kind(), y.Kind()
	if xk != yk {
		return false
	}

	switch xk {

	// Scalar kinds.
	case Kind_Null:
		return x.IsNull() == y.IsNull()
	case Kind_Bool:
		xv, err := x.AsBool()
		if err != nil {
			panic(err)
		}
		yv, err := y.AsBool()
		if err != nil {
			panic(err)
		}
		return xv == yv
	case Kind_Int:
		xv, err := x.AsInt()
		if err != nil {
			panic(err)
		}
		yv, err := y.AsInt()
		if err != nil {
			panic(err)
		}
		return xv == yv
	case Kind_Float:
		xv, err := x.AsFloat()
		if err != nil {
			panic(err)
		}
		yv, err := y.AsFloat()
		if err != nil {
			panic(err)
		}
		return xv == yv
	case Kind_String:
		xv, err := x.AsString()
		if err != nil {
			panic(err)
		}
		yv, err := y.AsString()
		if err != nil {
			panic(err)
		}
		return xv == yv
	case Kind_Bytes:
		xv, err := x.AsBytes()
		if err != nil {
			panic(err)
		}
		yv, err := y.AsBytes()
		if err != nil {
			panic(err)
		}
		return string(xv) == string(yv)
	case Kind_Link:
		xv, err := x.AsLink()
		if err != nil {
			panic(err)
		}
		yv, err := y.AsLink()
		if err != nil {
			panic(err)
		}
		// Links are just compared via ==.
		// This requires the types to exactly match,
		// and the values to be equal as per == too.
		// This will generally work,
		// as ipld-prime assumes link types to be consistent.
		return xv == yv

	// Recursive kinds.
	case Kind_Map:
		if x.Length() != y.Length() {
			return false
		}
		xitr := x.MapIterator()
		yitr := y.MapIterator()
		for !xitr.Done() && !yitr.Done() {
			xkey, xval, err := xitr.Next()
			if err != nil {
				panic(err)
			}
			ykey, yval, err := yitr.Next()
			if err != nil {
				panic(err)
			}
			if !DeepEqual(xkey, ykey) {
				return false
			}
			if !DeepEqual(xval, yval) {
				return false
			}
		}
		return true
	case Kind_List:
		if x.Length() != y.Length() {
			return false
		}
		xitr := x.ListIterator()
		yitr := y.ListIterator()
		for !xitr.Done() && !yitr.Done() {
			_, xval, err := xitr.Next()
			if err != nil {
				panic(err)
			}
			_, yval, err := yitr.Next()
			if err != nil {
				panic(err)
			}
			if !DeepEqual(xval, yval) {
				return false
			}
		}
		return true

	// As per the docs, other kinds such as Invalid are not deeply equal.
	default:
		return false
	}
}
