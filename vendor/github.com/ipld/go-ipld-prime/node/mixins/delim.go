package mixins

// This file is a little different than most of its siblings in this package.
// It's not really much of a "mixin".  More of a util function junkdrawer.
//
// Implementations of Data Model Nodes are unlikely to need these.
// Implementations of Schema-level Node *are* likely to need these, however.
//
// Our codegen implementation emits calls to these functions.
// (And having these functions in a package that's already an unconditional
// import in files emitted by codegen makes the codegen significantly simpler.)

import (
	"fmt"
	"strings"
)

// SplitExact is much like strings.Split but will error if the number of
// substrings is other than the expected count.
//
// SplitExact is used by the 'stringjoin' representation for structs.
//
// The 'count' parameter is a length.  In other words, if you expect
// the zero'th index to be present in the result, you should ask for
// a count of at least '1'.
// Using this function with 'count' less than 2 is rather strange.
func SplitExact(s string, sep string, count int) ([]string, error) {
	ss := strings.Split(s, sep)
	if len(ss) != count {
		return nil, fmt.Errorf("expected %d instances of the delimiter, found %d", count-1, len(ss)-1)
	}
	return ss, nil
}

// SplitN is an alias of strings.SplitN, which is only present here to
// make it usable in codegen packages without requiring conditional imports
// in the generation process.
func SplitN(s, sep string, n int) []string {
	return strings.SplitN(s, sep, n)
}
