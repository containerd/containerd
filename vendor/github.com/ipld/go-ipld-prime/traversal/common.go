package traversal

import (
	"context"
	"fmt"

	ipld "github.com/ipld/go-ipld-prime"
	"github.com/ipld/go-ipld-prime/schema"
)

// init sets all the values in TraveralConfig to reasonable defaults
// if they're currently the zero value.
//
// Note that you're absolutely going to need to replace the
// LinkLoader and LinkNodeBuilderChooser if you want automatic link traversal;
// the defaults return error and/or panic.
func (tc *Config) init() {
	if tc.Ctx == nil {
		tc.Ctx = context.Background()
	}
	if tc.LinkTargetNodePrototypeChooser == nil {
		tc.LinkTargetNodePrototypeChooser = func(lnk ipld.Link, lnkCtx ipld.LinkContext) (ipld.NodePrototype, error) {
			if tlnkNd, ok := lnkCtx.LinkNode.(schema.TypedLinkNode); ok {
				return tlnkNd.LinkTargetNodePrototype(), nil
			}
			return nil, fmt.Errorf("no LinkTargetNodePrototypeChooser configured")
		}
	}
}

func (prog *Progress) init() {
	if prog.Cfg == nil {
		prog.Cfg = &Config{}
	}
	prog.Cfg.init()
}

// asPathSegment figures out how to coerce a node into a PathSegment.
// If it's a typed node: we take its representation.  (Could be a struct with some string representation.)
// If it's a string or an int, that's it.
// Any other case will panic.  (If you're using this one keys returned by a MapIterator, though, you can ignore this possibility;
// any compliant map implementation should've already rejected that data long ago, and should not be able to yield it to you from an iterator.)
func asPathSegment(n ipld.Node) ipld.PathSegment {
	if n2, ok := n.(schema.TypedNode); ok {
		n = n2.Representation()
	}
	switch n.Kind() {
	case ipld.Kind_String:
		s, _ := n.AsString()
		return ipld.PathSegmentOfString(s)
	case ipld.Kind_Int:
		i, _ := n.AsInt()
		return ipld.PathSegmentOfInt(i)
	default:
		panic(fmt.Errorf("cannot get pathsegment from a %s", n.Kind()))
	}
}
