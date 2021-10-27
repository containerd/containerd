package traversal

import (
	"fmt"

	ipld "github.com/ipld/go-ipld-prime"
)

// Focus traverses a Node graph according to a path, reaches a single Node,
// and calls the given VisitFn on that reached node.
//
// This function is a helper function which starts a new traversal with default configuration.
// It cannot cross links automatically (since this requires configuration).
// Use the equivalent Focus function on the Progress structure
// for more advanced and configurable walks.
func Focus(n ipld.Node, p ipld.Path, fn VisitFn) error {
	return Progress{}.Focus(n, p, fn)
}

// Get is the equivalent of Focus, but returns the reached node (rather than invoking a callback at the target),
// and does not yield Progress information.
//
// This function is a helper function which starts a new traversal with default configuration.
// It cannot cross links automatically (since this requires configuration).
// Use the equivalent Get function on the Progress structure
// for more advanced and configurable walks.
func Get(n ipld.Node, p ipld.Path) (ipld.Node, error) {
	return Progress{}.Get(n, p)
}

// FocusedTransform traverses an ipld.Node graph, reaches a single Node,
// and calls the given TransformFn to decide what new node to replace the visited node with.
// A new Node tree will be returned (the original is unchanged).
//
// This function is a helper function which starts a new traversal with default configuration.
// It cannot cross links automatically (since this requires configuration).
// Use the equivalent FocusedTransform function on the Progress structure
// for more advanced and configurable walks.
func FocusedTransform(n ipld.Node, p ipld.Path, fn TransformFn, createParents bool) (ipld.Node, error) {
	return Progress{}.FocusedTransform(n, p, fn, createParents)
}

// Focus traverses a Node graph according to a path, reaches a single Node,
// and calls the given VisitFn on that reached node.
//
// Focus is a read-only traversal.
// See FocusedTransform if looking for a way to do an "update" to a Node.
//
// Provide configuration to this process using the Config field in the Progress object.
//
// This walk will automatically cross links, but requires some configuration
// with link loading functions to do so.
//
// Focus (and the other traversal functions) can be used again again inside the VisitFn!
// By using the traversal.Progress handed to the VisitFn,
// the Path recorded of the traversal so far will continue to be extended,
// and thus continued nested uses of Walk and Focus will see the fully contextualized Path.
func (prog Progress) Focus(n ipld.Node, p ipld.Path, fn VisitFn) error {
	n, err := prog.get(n, p, true)
	if err != nil {
		return err
	}
	return fn(prog, n)
}

// Get is the equivalent of Focus, but returns the reached node (rather than invoking a callback at the target),
// and does not yield Progress information.
//
// Provide configuration to this process using the Config field in the Progress object.
//
// This walk will automatically cross links, but requires some configuration
// with link loading functions to do so.
//
// If doing several traversals which are nested, consider using the Focus funcion in preference to Get;
// the Focus functions provide updated Progress objects which can be used to do nested traversals while keeping consistent track of progress,
// such that continued nested uses of Walk or Focus or Get will see the fully contextualized Path.
func (prog Progress) Get(n ipld.Node, p ipld.Path) (ipld.Node, error) {
	return prog.get(n, p, false)
}

// get is the internal implementation for Focus and Get.
// It *mutates* the Progress object it's called on, and returns reached nodes.
// For Get calls, trackProgress=false, which avoids some allocations for state tracking that's not needed by that call.
func (prog *Progress) get(n ipld.Node, p ipld.Path, trackProgress bool) (ipld.Node, error) {
	prog.init()
	segments := p.Segments()
	var prev ipld.Node // for LinkContext
	for i, seg := range segments {
		// Traverse the segment.
		switch n.Kind() {
		case ipld.Kind_Invalid:
			panic(fmt.Errorf("invalid node encountered at %q", p.Truncate(i)))
		case ipld.Kind_Map:
			next, err := n.LookupByString(seg.String())
			if err != nil {
				return nil, fmt.Errorf("error traversing segment %q on node at %q: %s", seg, p.Truncate(i), err)
			}
			prev, n = n, next
		case ipld.Kind_List:
			intSeg, err := seg.Index()
			if err != nil {
				return nil, fmt.Errorf("error traversing segment %q on node at %q: the segment cannot be parsed as a number and the node is a list", seg, p.Truncate(i))
			}
			next, err := n.LookupByIndex(intSeg)
			if err != nil {
				return nil, fmt.Errorf("error traversing segment %q on node at %q: %s", seg, p.Truncate(i), err)
			}
			prev, n = n, next
		default:
			return nil, fmt.Errorf("cannot traverse node at %q: %s", p.Truncate(i), fmt.Errorf("cannot traverse terminals"))
		}
		// Dereference any links.
		for n.Kind() == ipld.Kind_Link {
			lnk, _ := n.AsLink()
			lnkCtx := ipld.LinkContext{
				Ctx:        prog.Cfg.Ctx,
				LinkPath:   p.Truncate(i),
				LinkNode:   n,
				ParentNode: prev,
			}
			// Pick what in-memory format we will build.
			np, err := prog.Cfg.LinkTargetNodePrototypeChooser(lnk, lnkCtx)
			if err != nil {
				return nil, fmt.Errorf("error traversing node at %q: could not load link %q: %s", p.Truncate(i+1), lnk, err)
			}
			// Load link!
			prev = n
			n, err = prog.Cfg.LinkSystem.Load(lnkCtx, lnk, np)
			if err != nil {
				return nil, fmt.Errorf("error traversing node at %q: could not load link %q: %s", p.Truncate(i+1), lnk, err)
			}
			if trackProgress {
				prog.LastBlock.Path = p.Truncate(i + 1)
				prog.LastBlock.Link = lnk
			}
		}
	}
	if trackProgress {
		prog.Path = prog.Path.Join(p)
	}
	return n, nil
}

// FocusedTransform traverses an ipld.Node graph, reaches a single Node,
// and calls the given TransformFn to decide what new node to replace the visited node with.
// A new Node tree will be returned (the original is unchanged).
//
// If the TransformFn returns the same Node which it was called with,
// then the transform is a no-op, and the Node returned from the
// FocusedTransform call as a whole will also be the same as its starting Node.
//
// Otherwise, the reached node will be "replaced" with the new Node -- meaning
// that new intermediate nodes will be constructed to also replace each
// parent Node that was traversed to get here, thus propagating the changes in
// a copy-on-write fashion -- and the FocusedTransform function as a whole will
// return a new Node containing identical children except for those replaced.
//
// FocusedTransform can be used again inside the applied function!
// This kind of composition can be useful for doing batches of updates.
// E.g. if have a large Node graph which contains a 100-element list, and
// you want to replace elements 12, 32, and 95 of that list:
// then you should FocusedTransform to the list first, and inside that
// TransformFn's body, you can replace the entire list with a new one
// that is composed of copies of everything but those elements -- including
// using more TransformFn calls as desired to produce the replacement elements
// if it so happens that those replacement elements are easiest to construct
// by regarding them as incremental updates to the previous values.
// (This approach can also be used when doing other modifications like insertion
// or reordering -- which would otherwise be tricky to define, since
// each operation could change the meaning of subsequently used indexes.)
//
// As a special case, list appending is supported by using the path segment "-".
// (This is determined by the node it applies to -- if that path segment
// is applied to a map, it's just a regular map key of the string of dash.)
//
// Note that anything you can do with the Transform function, you can also
// do with regular Node and NodeBuilder usage directly.  Transform just
// does a large amount of the intermediate bookkeeping that's useful when
// creating new values which are partial updates to existing values.
//
func (prog Progress) FocusedTransform(n ipld.Node, p ipld.Path, fn TransformFn, createParents bool) (ipld.Node, error) {
	prog.init()
	nb := n.Prototype().NewBuilder()
	if err := prog.focusedTransform(n, nb, p, fn, createParents); err != nil {
		return nil, err
	}
	return nb.Build(), nil
}

// focusedTransform assumes that an update will actually happen, and as it recurses deeper,
// begins building an updated node tree.
//
// As implemented, this is not actually efficient if the update will be a no-op; it won't notice until it gets there.
func (prog Progress) focusedTransform(n ipld.Node, na ipld.NodeAssembler, p ipld.Path, fn TransformFn, createParents bool) error {
	if p.Len() == 0 {
		n2, err := fn(prog, n)
		if err != nil {
			return err
		}
		return na.AssignNode(n2)
	}
	seg, p2 := p.Shift()
	// Special branch for if we've entered createParent mode in an earlier step.
	//  This needs slightly different logic because there's no prior node to reference
	//   (and we wouldn't want to waste time creating a dummy one).
	if n == nil {
		ma, err := na.BeginMap(1)
		if err != nil {
			return err
		}
		prog.Path = prog.Path.AppendSegment(seg)
		if err := ma.AssembleKey().AssignString(seg.String()); err != nil {
			return err
		}
		if err := prog.focusedTransform(nil, ma.AssembleValue(), p2, fn, createParents); err != nil {
			return err
		}
		return ma.Finish()
	}
	// Handle node based on kind.
	//  If it's a recursive kind (map or list), we'll be recursing on it.
	//  If it's a link, load it!  And recurse on it.
	//  If it's a scalar kind (any of the rest), we'll... be erroring, actually;
	//   if we're at the end, it was already handled at the top of the function,
	//   so we only get to this case if we were expecting to go deeper.
	switch n.Kind() {
	case ipld.Kind_Map:
		ma, err := na.BeginMap(n.Length())
		if err != nil {
			return err
		}
		// Copy children over.  Replace the target (preserving its current position!) while doing this, if found.
		//  Note that we don't recurse into copying children (assuming AssignNode doesn't); this is as shallow/COW as the AssignNode implementation permits.
		var replaced bool
		for itr := n.MapIterator(); !itr.Done(); {
			k, v, err := itr.Next()
			if err != nil {
				return err
			}
			if err := ma.AssembleKey().AssignNode(k); err != nil {
				return err
			}
			if asPathSegment(k).Equals(seg) {
				prog.Path = prog.Path.AppendSegment(seg)
				if err := prog.focusedTransform(v, ma.AssembleValue(), p2, fn, createParents); err != nil {
					return err
				}
				replaced = true
			} else {
				if err := ma.AssembleValue().AssignNode(v); err != nil {
					return err
				}
			}
		}
		if replaced {
			return ma.Finish()
		}
		// If we didn't find the target yet: append it.
		//  If we're at the end, always do this;
		//  if we're in the middle, only do this if createParents mode is enabled.
		prog.Path = prog.Path.AppendSegment(seg)
		if p.Len() > 1 && !createParents {
			return fmt.Errorf("transform: parent position at %q did not exist (and createParents was false)", prog.Path)
		}
		if err := ma.AssembleKey().AssignString(seg.String()); err != nil {
			return err
		}
		if err := prog.focusedTransform(nil, ma.AssembleValue(), p2, fn, createParents); err != nil {
			return err
		}
		return ma.Finish()
	case ipld.Kind_List:
		la, err := na.BeginList(n.Length())
		if err != nil {
			return err
		}
		// First figure out if this path segment can apply to a list sanely at all.
		//  Simultaneously, get it in numeric format, so subsequent operations are cheaper.
		ti, err := seg.Index()
		if err != nil {
			if seg.String() == "-" {
				ti = -1
			} else {
				return fmt.Errorf("transform: cannot navigate path segment %q at %q because a list is here", seg, prog.Path)
			}
		}
		// Copy children over.  Replace the target (preserving its current position!) while doing this, if found.
		//  Note that we don't recurse into copying children (assuming AssignNode doesn't); this is as shallow/COW as the AssignNode implementation permits.
		var replaced bool
		for itr := n.ListIterator(); !itr.Done(); {
			i, v, err := itr.Next()
			if err != nil {
				return err
			}
			if ti == i {
				prog.Path = prog.Path.AppendSegment(seg)
				if err := prog.focusedTransform(v, la.AssembleValue(), p2, fn, createParents); err != nil {
					return err
				}
				replaced = true
			} else {
				if err := la.AssembleValue().AssignNode(v); err != nil {
					return err
				}
			}
		}
		if replaced {
			return la.Finish()
		}
		// If we didn't find the target yet: hopefully this was an append operation;
		//  if it wasn't, then it's index out of bounds.  We don't arbitrarily extend lists with filler.
		if ti >= 0 {
			return fmt.Errorf("transform: cannot navigate path segment %q at %q because it is beyond the list bounds", seg, prog.Path)
		}
		prog.Path = prog.Path.AppendSegment(ipld.PathSegmentOfInt(n.Length()))
		if err := prog.focusedTransform(nil, la.AssembleValue(), p2, fn, createParents); err != nil {
			return err
		}
		return la.Finish()
	case ipld.Kind_Link:
		lnkCtx := ipld.LinkContext{
			Ctx:        prog.Cfg.Ctx,
			LinkPath:   prog.Path,
			LinkNode:   n,
			ParentNode: nil, // TODO inconvenient that we don't have this.  maybe this whole case should be a helper function.
		}
		lnk, _ := n.AsLink()
		// Pick what in-memory format we will build.
		np, err := prog.Cfg.LinkTargetNodePrototypeChooser(lnk, lnkCtx)
		if err != nil {
			return fmt.Errorf("transform: error traversing node at %q: could not load link %q: %s", prog.Path, lnk, err)
		}
		// Load link!
		//  We'll use LinkSystem.Fill here rather than Load,
		//   because there's a nice opportunity to reuse the builder shortly.
		nb := np.NewBuilder()
		err = prog.Cfg.LinkSystem.Fill(lnkCtx, lnk, nb)
		if err != nil {
			return fmt.Errorf("transform: error traversing node at %q: could not load link %q: %s", prog.Path, lnk, err)
		}
		prog.LastBlock.Path = prog.Path
		prog.LastBlock.Link = lnk
		n = nb.Build()
		// Recurse.
		//  Start a new builder for this, using the same prototype we just used for loading the link.
		//   (Or more specifically: this is an opportunity for just resetting a builder and reusing memory!)
		//  When we come back... we'll have to engage serialization and storage on the new node!
		//  Path isn't updated here (neither progress nor to-go).
		nb.Reset()
		if err := prog.focusedTransform(n, nb, p, fn, createParents); err != nil {
			return err
		}
		n = nb.Build()
		lnk, err = prog.Cfg.LinkSystem.Store(lnkCtx, lnk.Prototype(), n)
		if err != nil {
			return fmt.Errorf("transform: error storing transformed node at %q: %s", prog.Path, err)
		}
		return na.AssignLink(lnk)
	default:
		return fmt.Errorf("transform: parent position at %q was a scalar, cannot go deeper", prog.Path)
	}
}
