package traversal

import (
	"fmt"

	ipld "github.com/ipld/go-ipld-prime"
	"github.com/ipld/go-ipld-prime/traversal/selector"
)

// WalkMatching walks a graph of Nodes, deciding which to visit by applying a Selector,
// and calling the given VisitFn on those that the Selector deems a match.
//
// This function is a helper function which starts a new walk with default configuration.
// It cannot cross links automatically (since this requires configuration).
// Use the equivalent WalkMatching function on the Progress structure
// for more advanced and configurable walks.
func WalkMatching(n ipld.Node, s selector.Selector, fn VisitFn) error {
	return Progress{}.WalkMatching(n, s, fn)
}

// WalkAdv is identical to WalkMatching, except it is called for *all* nodes
// visited (not just matching nodes), together with the reason for the visit.
// An AdvVisitFn is used instead of a VisitFn, so that the reason can be provided.
//
// This function is a helper function which starts a new walk with default configuration.
// It cannot cross links automatically (since this requires configuration).
// Use the equivalent WalkAdv function on the Progress structure
// for more advanced and configurable walks.
func WalkAdv(n ipld.Node, s selector.Selector, fn AdvVisitFn) error {
	return Progress{}.WalkAdv(n, s, fn)
}

// WalkTransforming walks a graph of Nodes, deciding which to alter by applying a Selector,
// and calls the given TransformFn to decide what new node to replace the visited node with.
// A new Node tree will be returned (the original is unchanged).
//
// This function is a helper function which starts a new walk with default configuration.
// It cannot cross links automatically (since this requires configuration).
// Use the equivalent WalkTransforming function on the Progress structure
// for more advanced and configurable walks.
func WalkTransforming(n ipld.Node, s selector.Selector, fn TransformFn) (ipld.Node, error) {
	return Progress{}.WalkTransforming(n, s, fn)
}

// WalkMatching walks a graph of Nodes, deciding which to visit by applying a Selector,
// and calling the given VisitFn on those that the Selector deems a match.
//
// WalkMatching is a read-only traversal.
// See WalkTransforming if looking for a way to do "updates" to a tree of nodes.
//
// Provide configuration to this process using the Config field in the Progress object.
//
// This walk will automatically cross links, but requires some configuration
// with link loading functions to do so.
//
// Traversals are defined as visiting a (node,path) tuple.
// This is important to note because when walking DAGs with Links,
// it means you may visit the same node multiple times
// due to having reached it via a different path.
// (You can prevent this by using a LinkLoader function which memoizes a set of
// already-visited Links, and returns a SkipMe when encountering them again.)
//
// WalkMatching (and the other traversal functions) can be used again again inside the VisitFn!
// By using the traversal.Progress handed to the VisitFn,
// the Path recorded of the traversal so far will continue to be extended,
// and thus continued nested uses of Walk and Focus will see the fully contextualized Path.
//
func (prog Progress) WalkMatching(n ipld.Node, s selector.Selector, fn VisitFn) error {
	prog.init()
	return prog.walkAdv(n, s, func(prog Progress, n ipld.Node, tr VisitReason) error {
		if tr != VisitReason_SelectionMatch {
			return nil
		}
		return fn(prog, n)
	})
}

// WalkAdv is identical to WalkMatching, except it is called for *all* nodes
// visited (not just matching nodes), together with the reason for the visit.
// An AdvVisitFn is used instead of a VisitFn, so that the reason can be provided.
//
func (prog Progress) WalkAdv(n ipld.Node, s selector.Selector, fn AdvVisitFn) error {
	prog.init()
	return prog.walkAdv(n, s, fn)
}

func (prog Progress) walkAdv(n ipld.Node, s selector.Selector, fn AdvVisitFn) error {
	if s.Decide(n) {
		if err := fn(prog, n, VisitReason_SelectionMatch); err != nil {
			return err
		}
	} else {
		if err := fn(prog, n, VisitReason_SelectionCandidate); err != nil {
			return err
		}
	}
	nk := n.Kind()
	switch nk {
	case ipld.Kind_Map, ipld.Kind_List: // continue
	default:
		return nil
	}
	attn := s.Interests()
	if attn == nil {
		return prog.walkAdv_iterateAll(n, s, fn)
	}
	return prog.walkAdv_iterateSelective(n, attn, s, fn)

}

func (prog Progress) walkAdv_iterateAll(n ipld.Node, s selector.Selector, fn AdvVisitFn) error {
	for itr := selector.NewSegmentIterator(n); !itr.Done(); {
		ps, v, err := itr.Next()
		if err != nil {
			return err
		}
		sNext := s.Explore(n, ps)
		if sNext != nil {
			progNext := prog
			progNext.Path = prog.Path.AppendSegment(ps)
			if v.Kind() == ipld.Kind_Link {
				lnk, _ := v.AsLink()
				progNext.LastBlock.Path = progNext.Path
				progNext.LastBlock.Link = lnk
				v, err = progNext.loadLink(v, n)
				if err != nil {
					if _, ok := err.(SkipMe); ok {
						return nil
					}
					return err
				}
			}

			err = progNext.walkAdv(v, sNext, fn)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (prog Progress) walkAdv_iterateSelective(n ipld.Node, attn []ipld.PathSegment, s selector.Selector, fn AdvVisitFn) error {
	for _, ps := range attn {
		v, err := n.LookupBySegment(ps)
		if err != nil {
			continue
		}
		sNext := s.Explore(n, ps)
		if sNext != nil {
			progNext := prog
			progNext.Path = prog.Path.AppendSegment(ps)
			if v.Kind() == ipld.Kind_Link {
				lnk, _ := v.AsLink()
				progNext.LastBlock.Path = progNext.Path
				progNext.LastBlock.Link = lnk
				v, err = progNext.loadLink(v, n)
				if err != nil {
					if _, ok := err.(SkipMe); ok {
						return nil
					}
					return err
				}
			}

			err = progNext.walkAdv(v, sNext, fn)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (prog Progress) loadLink(v ipld.Node, parent ipld.Node) (ipld.Node, error) {
	lnk, err := v.AsLink()
	if err != nil {
		return nil, err
	}
	lnkCtx := ipld.LinkContext{
		Ctx:        prog.Cfg.Ctx,
		LinkPath:   prog.Path,
		LinkNode:   v,
		ParentNode: parent,
	}
	// Pick what in-memory format we will build.
	np, err := prog.Cfg.LinkTargetNodePrototypeChooser(lnk, lnkCtx)
	if err != nil {
		return nil, fmt.Errorf("error traversing node at %q: could not load link %q: %s", prog.Path, lnk, err)
	}
	// Load link!
	n, err := prog.Cfg.LinkSystem.Load(lnkCtx, lnk, np)
	if err != nil {
		if _, ok := err.(SkipMe); ok {
			return nil, err
		}
		return nil, fmt.Errorf("error traversing node at %q: could not load link %q: %s", prog.Path, lnk, err)
	}
	return n, nil
}

// WalkTransforming walks a graph of Nodes, deciding which to alter by applying a Selector,
// and calls the given TransformFn to decide what new node to replace the visited node with.
// A new Node tree will be returned (the original is unchanged).
//
// If the TransformFn returns the same Node which it was called with,
// then the transform is a no-op; if every visited node is a no-op,
// then the root node returned from the walk as a whole will also be
// the same as its starting Node (no new memory will be used).
//
// When a Node is replaced, no further recursion of this walk will occur on its contents.
// (You can certainly do a additional traversals, including transforms,
// from inside the TransformFn while building the replacement node.)
//
// The prototype (that is, implementation) of Node returned will be the same as the
// prototype of the Nodes at the same positions in the existing tree
// (literally, builders used to construct any new needed intermediate nodes
// are chosen by asking the existing nodes about their prototype).
//
// This feature is not yet implemented.
func (prog Progress) WalkTransforming(n ipld.Node, s selector.Selector, fn TransformFn) (ipld.Node, error) {
	panic("TODO")
}
