package selector

import (
	"fmt"

	ipld "github.com/ipld/go-ipld-prime"
)

// ExploreUnion allows selection to continue with two or more distinct selectors
// while exploring the same tree of data.
//
// ExploreUnion can be used to apply a Matcher on one node (causing it to
// be considered part of a (possibly labelled) result set), while simultaneously
// continuing to explore deeper parts of the tree with another selector,
// for example.
type ExploreUnion struct {
	Members []Selector
}

// Interests for ExploreUnion is:
// - nil (aka all) if any member selector has nil interests
// - the union of values returned by all member selectors otherwise
func (s ExploreUnion) Interests() []ipld.PathSegment {
	// Check for any high-cardinality selectors first; if so, shortcircuit.
	//  (n.b. we're assuming the 'Interests' method is cheap here.)
	for _, m := range s.Members {
		if m.Interests() == nil {
			return nil
		}
	}
	// Accumulate the whitelist of interesting path segments.
	// TODO: Dedup?
	v := []ipld.PathSegment{}
	for _, m := range s.Members {
		v = append(v, m.Interests()...)
	}
	return v
}

// Explore for a Union selector calls explore for each member selector
// and returns:
// - a new union selector if more than one member returns a selector
// - if exactly one member returns a selector, that selector
// - nil if no members return a selector
func (s ExploreUnion) Explore(n ipld.Node, p ipld.PathSegment) Selector {
	// TODO: memory efficient?
	nonNilResults := make([]Selector, 0, len(s.Members))
	for _, member := range s.Members {
		resultSelector := member.Explore(n, p)
		if resultSelector != nil {
			nonNilResults = append(nonNilResults, resultSelector)
		}
	}
	if len(nonNilResults) == 0 {
		return nil
	}
	if len(nonNilResults) == 1 {
		return nonNilResults[0]
	}
	return ExploreUnion{nonNilResults}
}

// Decide returns true for a Union selector if any of the member selectors
// return true
func (s ExploreUnion) Decide(n ipld.Node) bool {
	for _, m := range s.Members {
		if m.Decide(n) {
			return true
		}
	}
	return false
}

// ParseExploreUnion assembles a Selector
// from an ExploreUnion selector node
func (pc ParseContext) ParseExploreUnion(n ipld.Node) (Selector, error) {
	if n.Kind() != ipld.Kind_List {
		return nil, fmt.Errorf("selector spec parse rejected: explore union selector must be a list")
	}
	x := ExploreUnion{
		make([]Selector, 0, n.Length()),
	}
	for itr := n.ListIterator(); !itr.Done(); {
		_, v, err := itr.Next()
		if err != nil {
			return nil, fmt.Errorf("error during selector spec parse: %s", err)
		}
		member, err := pc.ParseSelector(v)
		if err != nil {
			return nil, err
		}
		x.Members = append(x.Members, member)
	}
	return x, nil
}
