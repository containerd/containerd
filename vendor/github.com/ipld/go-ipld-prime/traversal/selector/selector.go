package selector

import (
	"fmt"

	ipld "github.com/ipld/go-ipld-prime"
)

// Selector is a "compiled" and executable IPLD Selector.
// It can be put to work with functions like traversal.Walk,
// which will use the Selector's guidance to decide how to traverse an IPLD data graph.
//
// A Selector is created by parsing an IPLD Data Model document that declares a Selector
// (this is accomplished with functions like CompileSelector).
// Alternatively, there is a builder subpackage,
// which is useful if you would rather create the Selector declaration programmatically in golang.
//
// There is no way to go backwards from this "compiled" Selector type into the declarative IPLD data model information that produced it.
// That declaration information is discarded after compilation in order to limit the amount of memory held.
// Therefore, if you're building APIs about Selector composition, keep in mind that
// you'll probably want to approach this be composing the Data Model declaration documents,
// not be composing this type, which is only for the "compiled" result.
type Selector interface {
	Interests() []ipld.PathSegment                // returns the segments we're likely interested in **or nil** if we're a high-cardinality or expression based matcher and need all segments proposed to us.
	Explore(ipld.Node, ipld.PathSegment) Selector // explore one step -- iteration comes from outside (either whole node, or by following suggestions of Interests).  returns nil if no interest.  you have to traverse to the next node yourself (the selector doesn't do it for you because you might be considering multiple selection reasons at the same time).
	Decide(ipld.Node) bool
}

// REVIEW: do ParsedParent and ParseContext need to be exported?  They're mostly used during the compilation process.

// ParsedParent is created whenever you are parsing a selector node that may have
// child selectors nodes that need to know it
type ParsedParent interface {
	Link(s Selector) bool
}

// ParseContext tracks the progress when parsing a selector
type ParseContext struct {
	parentStack []ParsedParent
}

// CompileSelector accepts an ipld.Node which should contain data that declares a Selector.
// The data layout expected for this declaration is documented in https://ipld.io/specs/selectors/ .
//
// If the Selector is compiled successfully, it is returned.
// Otherwise, if the given data Node doesn't match the expected shape for a Selector declaration,
// or there are any other problems compiling the selector
// (such as a recursion edge with no enclosing recursion declaration, etc),
// then nil and an error will be returned.
func CompileSelector(dmt ipld.Node) (Selector, error) {
	return ParseContext{}.ParseSelector(dmt)
}

// ParseSelector is an alias for CompileSelector, and is deprecated.
// Prefer CompileSelector.
func ParseSelector(dmt ipld.Node) (Selector, error) {
	return CompileSelector(dmt)
}

// ParseSelector creates a Selector from an IPLD Selector Node with the given context
func (pc ParseContext) ParseSelector(n ipld.Node) (Selector, error) {
	if n.Kind() != ipld.Kind_Map {
		return nil, fmt.Errorf("selector spec parse rejected: selector is a keyed union and thus must be a map")
	}
	if n.Length() != 1 {
		return nil, fmt.Errorf("selector spec parse rejected: selector is a keyed union and thus must be single-entry map")
	}
	kn, v, _ := n.MapIterator().Next()
	kstr, _ := kn.AsString()
	// Switch over the single key to determine which selector body comes next.
	//  (This switch is where the keyed union discriminators concretely happen.)
	switch kstr {
	case SelectorKey_ExploreFields:
		return pc.ParseExploreFields(v)
	case SelectorKey_ExploreAll:
		return pc.ParseExploreAll(v)
	case SelectorKey_ExploreIndex:
		return pc.ParseExploreIndex(v)
	case SelectorKey_ExploreRange:
		return pc.ParseExploreRange(v)
	case SelectorKey_ExploreUnion:
		return pc.ParseExploreUnion(v)
	case SelectorKey_ExploreRecursive:
		return pc.ParseExploreRecursive(v)
	case SelectorKey_ExploreRecursiveEdge:
		return pc.ParseExploreRecursiveEdge(v)
	case SelectorKey_Matcher:
		return pc.ParseMatcher(v)
	default:
		return nil, fmt.Errorf("selector spec parse rejected: %q is not a known member of the selector union", kstr)
	}
}

// PushParent puts a parent onto the stack of parents for a parse context
func (pc ParseContext) PushParent(parent ParsedParent) ParseContext {
	l := len(pc.parentStack)
	parents := make([]ParsedParent, 0, l+1)
	parents = append(parents, parent)
	parents = append(parents, pc.parentStack...)
	return ParseContext{parents}
}

// SegmentIterator iterates either a list or a map, generating PathSegments
// instead of indexes or keys
type SegmentIterator interface {
	Next() (pathSegment ipld.PathSegment, value ipld.Node, err error)
	Done() bool
}

// NewSegmentIterator generates a new iterator based on the node type
func NewSegmentIterator(n ipld.Node) SegmentIterator {
	if n.Kind() == ipld.Kind_List {
		return listSegmentIterator{n.ListIterator()}
	}
	return mapSegmentIterator{n.MapIterator()}
}

type listSegmentIterator struct {
	ipld.ListIterator
}

func (lsi listSegmentIterator) Next() (pathSegment ipld.PathSegment, value ipld.Node, err error) {
	i, v, err := lsi.ListIterator.Next()
	return ipld.PathSegmentOfInt(i), v, err
}

func (lsi listSegmentIterator) Done() bool {
	return lsi.ListIterator.Done()
}

type mapSegmentIterator struct {
	ipld.MapIterator
}

func (msi mapSegmentIterator) Next() (pathSegment ipld.PathSegment, value ipld.Node, err error) {
	k, v, err := msi.MapIterator.Next()
	if err != nil {
		return ipld.PathSegment{}, v, err
	}
	kstr, _ := k.AsString()
	return ipld.PathSegmentOfString(kstr), v, err
}

func (msi mapSegmentIterator) Done() bool {
	return msi.MapIterator.Done()
}
