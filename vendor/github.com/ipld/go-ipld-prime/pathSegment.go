package ipld

import (
	"strconv"
)

// PathSegment can describe either a key in a map, or an index in a list.
//
// Create a PathSegment via either ParsePathSegment, PathSegmentOfString,
// or PathSegmentOfInt; or, via one of the constructors of Path,
// which will implicitly create PathSegment internally.
// Using PathSegment's natural zero value directly is discouraged
// (it will act like ParsePathSegment("0"), which likely not what you'd expect).
//
// Path segments are "stringly typed" -- they may be interpreted as either strings or ints depending on context.
// A path segment of "123" will be used as a string when traversing a node of map kind;
// and it will be converted to an integer when traversing a node of list kind.
// (If a path segment string cannot be parsed to an int when traversing a node of list kind, then traversal will error.)
// It is not possible to ask which kind (string or integer) a PathSegment is, because that is not defined -- this is *only* intepreted contextually.
//
// Internally, PathSegment will store either a string or an integer,
// depending on how it was constructed,
// and will automatically convert to the other on request.
// (This means if two pieces of code communicate using PathSegment,
// one producing ints and the other expecting ints,
// then they will work together efficiently.)
// PathSegment in a Path produced by ParsePath generally have all strings internally,
// because there is no distinction possible when parsing a Path string
// (and attempting to pre-parse all strings into ints "just in case" would waste time in almost all cases).
//
// Be cautious of attempting to use PathSegment as a map key!
// Due to the implementation detail of internal storage, it's possible for
// PathSegment values which are "equal" per PathSegment.Equal's definition
// to still be unequal in the eyes of golang's native maps.
// You should probably use the string values of the PathSegment as map keys.
// (This has the additional bonus of hitting a special fastpath that the golang
// built-in maps have specifically for plain string keys.)
//
type PathSegment struct {
	/*
		A quick implementation note about the Go compiler and "union" semantics:

		There are roughly two ways to do "union" semantics in Go.

		The first is to make a struct with each of the values.

		The second is to make an interface and use an unexported method to keep it closed.

		The second tactic provides somewhat nicer semantics to the programmer.
		(Namely, it's clearly impossible to have two inhabitants, which is... the point.)
		The downside is... putting things in interfaces generally incurs an allocation
		(grep your assembly output for "runtime.conv*").

		The first tactic looks kludgier, and would seem to waste memory
		(the struct reserves space for each possible value, even though the semantic is that only one may be non-zero).
		However, in most cases, more *bytes* are cheaper than more *allocs* --
		garbage collection costs are domininated by alloc count, not alloc size.

		Because PathSegment is something we expect to put in fairly "hot" paths,
		we're using the first tactic.

		(We also currently get away with having no extra discriminator bit
		because we use a signed int for indexes, and negative values aren't valid there,
		and thus we can use it as a sentinel value.
		(Fun note: Empty strings were originally used for this sentinel,
		but it turns out empty strings are valid PathSegment themselves, so!))
	*/

	s string
	i int64
}

// ParsePathSegment parses a string into a PathSegment,
// handling any escaping if present.
// (Note: there is currently no escaping specified for PathSegments,
// so this is currently functionally equivalent to PathSegmentOfString.)
func ParsePathSegment(s string) PathSegment {
	return PathSegment{s: s, i: -1}
}

// PathSegmentOfString boxes a string into a PathSegment.
// It does not attempt to parse any escaping; use ParsePathSegment for that.
func PathSegmentOfString(s string) PathSegment {
	return PathSegment{s: s, i: -1}
}

// PathSegmentOfString boxes an int into a PathSegment.
func PathSegmentOfInt(i int64) PathSegment {
	return PathSegment{i: i}
}

// containsString is unexported because we use it to see what our *storage* form is,
// but this is considered an implementation detail that's non-semantic.
// If it returns false, it implicitly means "containsInt", as these are the only options.
func (ps PathSegment) containsString() bool {
	return ps.i < 0
}

// String returns the PathSegment as a string.
func (ps PathSegment) String() string {
	switch ps.containsString() {
	case true:
		return ps.s
	case false:
		return strconv.FormatInt(ps.i, 10)
	}
	panic("unreachable")
}

// Index returns the PathSegment as an integer,
// or returns an error if the segment is a string that can't be parsed as an int.
func (ps PathSegment) Index() (int64, error) {
	switch ps.containsString() {
	case true:
		return strconv.ParseInt(ps.s, 10, 64)
	case false:
		return ps.i, nil
	}
	panic("unreachable")
}

// Equals checks if two PathSegment values are equal.
//
// Because PathSegment is "stringly typed", this comparison does not
// regard if one of the segments is stored as a string and one is stored as an int;
// if string values of two segments are equal, they are "equal" overall.
// In other words, `PathSegmentOfInt(2).Equals(PathSegmentOfString("2")) == true`!
// (You should still typically prefer this method over converting two segments
// to string and comparing those, because even though that may be functionally
// correct, this method will be faster if they're both ints internally.)
func (x PathSegment) Equals(o PathSegment) bool {
	if !x.containsString() && !o.containsString() {
		return x.i == o.i
	}
	return x.String() == o.String()
}
