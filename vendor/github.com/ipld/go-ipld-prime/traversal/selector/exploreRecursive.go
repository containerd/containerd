package selector

import (
	"fmt"

	ipld "github.com/ipld/go-ipld-prime"
)

// ExploreRecursive traverses some structure recursively.
// To guide this exploration, it uses a "sequence", which is another Selector
// tree; some leaf node in this sequence should contain an ExploreRecursiveEdge
// selector, which denotes the place recursion should occur.
//
// In implementation, whenever evaluation reaches an ExploreRecursiveEdge marker
// in the recursion sequence's Selector tree, the implementation logically
// produces another new Selector which is a copy of the original
// ExploreRecursive selector, but with a decremented maxDepth parameter, and
// continues evaluation thusly.
//
// It is not valid for an ExploreRecursive selector's sequence to contain
// no instances of ExploreRecursiveEdge; it *is* valid for it to contain
// more than one ExploreRecursiveEdge.
//
// ExploreRecursive can contain a nested ExploreRecursive!
// This is comparable to a nested for-loop.
// In these cases, any ExploreRecursiveEdge instance always refers to the
// nearest parent ExploreRecursive (in other words, ExploreRecursiveEdge can
// be thought of like the 'continue' statement, or end of a for-loop body;
// it is *not* a 'goto' statement).
//
// Be careful when using ExploreRecursive with a large maxDepth parameter;
// it can easily cause very large traversals (especially if used in combination
// with selectors like ExploreAll inside the sequence).
type ExploreRecursive struct {
	sequence Selector       // selector for element we're interested in
	current  Selector       // selector to apply to the current node
	limit    RecursionLimit // the limit for this recursive selector
	stopAt   *Condition     // a condition for not exploring the node or children
}

// RecursionLimit_Mode is an enum that represents the type of a recursion limit
// -- either "depth" or "none" for now
type RecursionLimit_Mode uint8

const (
	// RecursionLimit_None means there is no recursion limit
	RecursionLimit_None RecursionLimit_Mode = 0
	// RecursionLimit_Depth mean recursion stops after the recursive selector
	// is copied to a given depth
	RecursionLimit_Depth RecursionLimit_Mode = 1
)

// RecursionLimit is a union type that captures all data about the recursion
// limit (both its type and data specific to the type)
type RecursionLimit struct {
	mode  RecursionLimit_Mode
	depth int64
}

// Mode returns the type for this recursion limit
func (rl RecursionLimit) Mode() RecursionLimit_Mode {
	return rl.mode
}

// Depth returns the depth for a depth recursion limit, or 0 otherwise
func (rl RecursionLimit) Depth() int64 {
	if rl.mode != RecursionLimit_Depth {
		return 0
	}
	return rl.depth
}

// RecursionLimitDepth returns a depth limited recursion to the given depth
func RecursionLimitDepth(depth int64) RecursionLimit {
	return RecursionLimit{RecursionLimit_Depth, depth}
}

// RecursionLimitNone return recursion with no limit
func RecursionLimitNone() RecursionLimit {
	return RecursionLimit{RecursionLimit_None, 0}
}

// Interests for ExploreRecursive is empty (meaning traverse everything)
func (s ExploreRecursive) Interests() []ipld.PathSegment {
	return s.current.Interests()
}

// Explore returns the node's selector for all fields
func (s ExploreRecursive) Explore(n ipld.Node, p ipld.PathSegment) Selector {
	if s.stopAt != nil && s.stopAt.Match(n) {
		return nil
	}

	nextSelector := s.current.Explore(n, p)
	limit := s.limit

	if nextSelector == nil {
		return nil
	}
	if !s.hasRecursiveEdge(nextSelector) {
		return ExploreRecursive{s.sequence, nextSelector, limit, s.stopAt}
	}
	switch limit.mode {
	case RecursionLimit_Depth:
		if limit.depth < 2 {
			return s.replaceRecursiveEdge(nextSelector, nil)
		}
		return ExploreRecursive{s.sequence, s.replaceRecursiveEdge(nextSelector, s.sequence), RecursionLimit{RecursionLimit_Depth, limit.depth - 1}, s.stopAt}
	case RecursionLimit_None:
		return ExploreRecursive{s.sequence, s.replaceRecursiveEdge(nextSelector, s.sequence), limit, s.stopAt}
	default:
		panic("Unsupported recursion limit type")
	}
}

func (s ExploreRecursive) hasRecursiveEdge(nextSelector Selector) bool {
	_, isRecursiveEdge := nextSelector.(ExploreRecursiveEdge)
	if isRecursiveEdge {
		return true
	}
	exploreUnion, isUnion := nextSelector.(ExploreUnion)
	if isUnion {
		for _, selector := range exploreUnion.Members {
			if s.hasRecursiveEdge(selector) {
				return true
			}
		}
	}
	return false
}

func (s ExploreRecursive) replaceRecursiveEdge(nextSelector Selector, replacement Selector) Selector {
	_, isRecursiveEdge := nextSelector.(ExploreRecursiveEdge)
	if isRecursiveEdge {
		return replacement
	}
	exploreUnion, isUnion := nextSelector.(ExploreUnion)
	if isUnion {
		replacementMembers := make([]Selector, 0, len(exploreUnion.Members))
		for _, selector := range exploreUnion.Members {
			newSelector := s.replaceRecursiveEdge(selector, replacement)
			if newSelector != nil {
				replacementMembers = append(replacementMembers, newSelector)
			}
		}
		if len(replacementMembers) == 0 {
			return nil
		}
		if len(replacementMembers) == 1 {
			return replacementMembers[0]
		}
		return ExploreUnion{replacementMembers}
	}
	return nextSelector
}

// Decide if a node directly matches
func (s ExploreRecursive) Decide(n ipld.Node) bool {
	return s.current.Decide(n)
}

type exploreRecursiveContext struct {
	edgesFound int
}

func (erc *exploreRecursiveContext) Link(s Selector) bool {
	_, ok := s.(ExploreRecursiveEdge)
	if ok {
		erc.edgesFound++
	}
	return ok
}

// ParseExploreRecursive assembles a Selector from a ExploreRecursive selector node
func (pc ParseContext) ParseExploreRecursive(n ipld.Node) (Selector, error) {
	if n.Kind() != ipld.Kind_Map {
		return nil, fmt.Errorf("selector spec parse rejected: selector body must be a map")
	}

	limitNode, err := n.LookupByString(SelectorKey_Limit)
	if err != nil {
		return nil, fmt.Errorf("selector spec parse rejected: limit field must be present in ExploreRecursive selector")
	}
	limit, err := parseLimit(limitNode)
	if err != nil {
		return nil, err
	}
	sequence, err := n.LookupByString(SelectorKey_Sequence)
	if err != nil {
		return nil, fmt.Errorf("selector spec parse rejected: sequence field must be present in ExploreRecursive selector")
	}
	erc := &exploreRecursiveContext{}
	selector, err := pc.PushParent(erc).ParseSelector(sequence)
	if err != nil {
		return nil, err
	}
	if erc.edgesFound == 0 {
		return nil, fmt.Errorf("selector spec parse rejected: ExploreRecursive must have at least one ExploreRecursiveEdge")
	}
	var stopCondition *Condition
	stop, err := n.LookupByString(SelectorKey_StopAt)
	if err == nil {
		condition, err := pc.ParseCondition(stop)
		if err != nil {
			return nil, err
		}
		stopCondition = &condition
	}
	return ExploreRecursive{selector, selector, limit, stopCondition}, nil
}

func parseLimit(n ipld.Node) (RecursionLimit, error) {
	if n.Kind() != ipld.Kind_Map {
		return RecursionLimit{}, fmt.Errorf("selector spec parse rejected: limit in ExploreRecursive is a keyed union and thus must be a map")
	}
	if n.Length() != 1 {
		return RecursionLimit{}, fmt.Errorf("selector spec parse rejected: limit in ExploreRecursive is a keyed union and thus must be a single-entry map")
	}
	kn, v, _ := n.MapIterator().Next()
	kstr, _ := kn.AsString()
	switch kstr {
	case SelectorKey_LimitDepth:
		maxDepthValue, err := v.AsInt()
		if err != nil {
			return RecursionLimit{}, fmt.Errorf("selector spec parse rejected: limit field of type depth must be a number in ExploreRecursive selector")
		}
		return RecursionLimit{RecursionLimit_Depth, maxDepthValue}, nil
	case SelectorKey_LimitNone:
		return RecursionLimit{RecursionLimit_None, 0}, nil
	default:
		return RecursionLimit{}, fmt.Errorf("selector spec parse rejected: %q is not a known member of the limit union in ExploreRecursive", kstr)
	}
}
