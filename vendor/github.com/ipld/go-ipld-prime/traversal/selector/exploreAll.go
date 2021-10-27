package selector

import (
	"fmt"

	ipld "github.com/ipld/go-ipld-prime"
)

// ExploreAll is similar to a `*` -- it traverses all elements of an array,
// or all entries in a map, and applies a next selector to the reached nodes.
type ExploreAll struct {
	next Selector // selector for element we're interested in
}

// Interests for ExploreAll is nil (meaning traverse everything)
func (s ExploreAll) Interests() []ipld.PathSegment {
	return nil
}

// Explore returns the node's selector for all fields
func (s ExploreAll) Explore(n ipld.Node, p ipld.PathSegment) Selector {
	return s.next
}

// Decide always returns false because this is not a matcher
func (s ExploreAll) Decide(n ipld.Node) bool {
	return false
}

// ParseExploreAll assembles a Selector from a ExploreAll selector node
func (pc ParseContext) ParseExploreAll(n ipld.Node) (Selector, error) {
	if n.Kind() != ipld.Kind_Map {
		return nil, fmt.Errorf("selector spec parse rejected: selector body must be a map")
	}
	next, err := n.LookupByString(SelectorKey_Next)
	if err != nil {
		return nil, fmt.Errorf("selector spec parse rejected: next field must be present in ExploreAll selector")
	}
	selector, err := pc.ParseSelector(next)
	if err != nil {
		return nil, err
	}
	return ExploreAll{selector}, nil
}
