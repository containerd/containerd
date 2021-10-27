package ipld

import (
	"strings"
)

// Path describes a series of steps across a tree or DAG of Node,
// where each segment in the path is a map key or list index
// (literaly, Path is a slice of PathSegment values).
// Path is used in describing progress in a traversal; and
// can also be used as an instruction for traversing from one Node to another.
// Path values will also often be encountered as part of error messages.
//
// (Note that Paths are useful as an instruction for traversing from
// *one* Node to *one* other Node; to do a walk from one Node and visit
// *several* Nodes based on some sort of pattern, look to IPLD Selectors,
// and the 'traversal/selector' package in this project.)
//
// Path values are always relative.
// Observe how 'traversal.Focus' requires both a Node and a Path argument --
// where to start, and where to go, respectively.
// Similarly, error values which include a Path will be speaking in reference
// to the "starting Node" in whatever context they arose from.
//
// The canonical form of a Path is as a list of PathSegment.
// Each PathSegment is a string; by convention, the string should be
// in UTF-8 encoding and use NFC normalization, but all operations
// will regard the string as its constituent eight-bit bytes.
//
// There are no illegal or magical characters in IPLD Paths
// (in particular, do not mistake them for UNIX system paths).
// IPLD Paths can only go down: that is, each segment must traverse one node.
// There is no ".." which means "go up";
// and there is no "." which means "stay here".
// IPLD Paths have no magic behavior around characters such as "~".
// IPLD Paths do not have a concept of "globs" nor behave specially
// for a path segment string of "*" (but you may wish to see 'Selectors'
// for globbing-like features that traverse over IPLD data).
//
// An empty string is a valid PathSegment.
// (This leads to some unfortunate complications when wishing to represent
// paths in a simple string format; however, consider that maps do exist
// in serialized data in the wild where an empty string is used as the key:
// it is important we be able to correctly describe and address this!)
//
// A string containing "/" (or even being simply "/"!) is a valid PathSegment.
// (As with empty strings, this is unfortunate (in particular, because it
// very much doesn't match up well with expectations popularized by UNIX-like
// filesystems); but, as with empty strings, maps which contain such a key
// certainly exist, and it is important that we be able to regard them!)
//
// A string starting, ending, or otherwise containing the NUL (\x00) byte
// is also a valid PathSegment.  This follows from the rule of "a string is
// regarded as its constituent eight-bit bytes": an all-zero byte is not exceptional.
// In golang, this doesn't pose particular difficulty, but note this would be
// of marked concern for languages which have "C-style nul-terminated strings".
//
// For an IPLD Path to be represented as a string, an encoding system
// including escaping is necessary.  At present, there is not a single
// canonical specification for such an escaping; we expect to decide one
// in the future, but this is not yet settled and done.
// (This implementation has a 'String' method, but it contains caveats
// and may be ambiguous for some content.  This may be fixed in the future.)
type Path struct {
	segments []PathSegment
}

// NewPath returns a Path composed of the given segments.
//
// This constructor function does a defensive copy,
// in case your segments slice should mutate in the future.
// (Use NewPathNocopy if this is a performance concern,
// and you're sure you know what you're doing.)
func NewPath(segments []PathSegment) Path {
	p := Path{make([]PathSegment, len(segments))}
	copy(p.segments, segments)
	return p
}

// NewPathNocopy is identical to NewPath but trusts that
// the segments slice you provide will not be mutated.
func NewPathNocopy(segments []PathSegment) Path {
	return Path{segments}
}

// ParsePath converts a string to an IPLD Path, doing a basic parsing of the
// string using "/" as a delimiter to produce a segmented Path.
// This is a handy, but not a general-purpose nor spec-compliant (!),
// way to create a Path: it cannot represent all valid paths.
//
// Multiple subsequent "/" characters will be silently collapsed.
// E.g., `"foo///bar"` will be treated equivalently to `"foo/bar"`.
// Prefixed and suffixed extraneous "/" characters are also discarded.
// This makes this constructor incapable of handling some possible Path values
// (specifically: paths with empty segements cannot be created with this constructor).
//
// There is no escaping mechanism used by this function.
// This makes this constructor incapable of handling some possible Path values
// (specifically, a path segment containing "/" cannot be created, because it
// will always be intepreted as a segment separator).
//
// No other "cleaning" of the path occurs.  See the documentation of the Path struct;
// in particular, note that ".." does not mean "go up", nor does "." mean "stay here" --
// correspondingly, there isn't anything to "clean" in the same sense as
// 'filepath.Clean' from the standard library filesystem path packages would.
//
// If the provided string contains unprintable characters, or non-UTF-8
// or non-NFC-canonicalized bytes, no remark will be made about this,
// and those bytes will remain part of the PathSegments in the resulting Path.
func ParsePath(pth string) Path {
	// FUTURE: we should probably have some escaping mechanism which makes
	//  it possible to encode a slash in a segment.  Specification needed.
	ss := strings.FieldsFunc(pth, func(r rune) bool { return r == '/' })
	ssl := len(ss)
	p := Path{make([]PathSegment, ssl)}
	for i := 0; i < ssl; i++ {
		p.segments[i] = PathSegmentOfString(ss[i])
	}
	return p
}

// String representation of a Path is simply the join of each segment with '/'.
// It does not include a leading nor trailing slash.
//
// This is a handy, but not a general-purpose nor spec-compliant (!),
// way to reduce a Path to a string.
// There is no escaping mechanism used by this function,
// and as a result, not all possible valid Path values (such as those with
// empty segments or with segments containing "/") can be encoded unambiguously.
// For Path values containing these problematic segments, ParsePath applied
// to the string returned from this function may return a nonequal Path value.
//
// No escaping for unprintable characters is provided.
// No guarantee that the resulting string is UTF-8 nor NFC canonicalized
// is provided unless all the constituent PathSegment had those properties.
func (p Path) String() string {
	l := len(p.segments)
	if l == 0 {
		return ""
	}
	sb := strings.Builder{}
	for i := 0; i < l-1; i++ {
		sb.WriteString(p.segments[i].String())
		sb.WriteByte('/')
	}
	sb.WriteString(p.segments[l-1].String())
	return sb.String()
}

// Segments returns a slice of the path segment strings.
//
// It is not lawful to mutate nor append the returned slice.
func (p Path) Segments() []PathSegment {
	return p.segments
}

// Len returns the number of segments in this path.
//
// Zero segments means the path refers to "the current node".
// One segment means it refers to a child of the current node; etc.
func (p Path) Len() int {
	return len(p.segments)
}

// Join creates a new path composed of the concatenation of this and the given path's segments.
func (p Path) Join(p2 Path) Path {
	combinedSegments := make([]PathSegment, len(p.segments)+len(p2.segments))
	copy(combinedSegments, p.segments)
	copy(combinedSegments[len(p.segments):], p2.segments)
	p.segments = combinedSegments
	return p
}

// AppendSegmentString is as per Join, but a shortcut when appending single segments using strings.
func (p Path) AppendSegment(ps PathSegment) Path {
	l := len(p.segments)
	combinedSegments := make([]PathSegment, l+1)
	copy(combinedSegments, p.segments)
	combinedSegments[l] = ps
	p.segments = combinedSegments
	return p
}

// AppendSegmentString is as per Join, but a shortcut when appending single segments using strings.
func (p Path) AppendSegmentString(ps string) Path {
	return p.AppendSegment(PathSegmentOfString(ps))
}

// Parent returns a path with the last of its segments popped off (or
// the zero path if it's already empty).
func (p Path) Parent() Path {
	if len(p.segments) == 0 {
		return Path{}
	}
	return Path{p.segments[0 : len(p.segments)-1]}
}

// Truncate returns a path with only as many segments remaining as requested.
func (p Path) Truncate(i int) Path {
	return Path{p.segments[0:i]}
}

// Last returns the trailing segment of the path.
func (p Path) Last() PathSegment {
	if len(p.segments) < 1 {
		return PathSegment{}
	}
	return p.segments[len(p.segments)-1]
}

// Shift returns the first segment of the path together with the remaining path after that first segment.
// If applied to a zero-length path, it returns an empty segment and the same zero-length path.
func (p Path) Shift() (PathSegment, Path) {
	if len(p.segments) < 1 {
		return PathSegment{}, Path{}
	}
	return p.segments[0], Path{p.segments[1:]}
}
