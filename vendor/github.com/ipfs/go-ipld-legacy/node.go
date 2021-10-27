package ipldlegacy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	blocks "github.com/ipfs/go-block-format"
	format "github.com/ipfs/go-ipld-format"
	ipld "github.com/ipld/go-ipld-prime"
	"github.com/ipld/go-ipld-prime/codec/dagjson"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
	"github.com/ipld/go-ipld-prime/traversal"
)

type errorType string

func (e errorType) Error() string {
	return string(e)
}

func ErrorAtPathSegment(seg ipld.PathSegment, base ipld.Path, baseError error) error {
	return fmt.Errorf("error traversing segement %q on node at %q: %w", seg, base, baseError)
}

func ErrorAtPath(base ipld.Path, baseError error) error {
	return fmt.Errorf("error traversing at %q: %w", base, baseError)
}

const (
	// ErrNonLink is returned when ResolveLink is called on a node that is not a link
	ErrNonLink = errorType("non-link found at given path")
	// ErrNoSuchLink is returned when ResolveLink is called on a path that does not exist
	ErrNoSuchLink = errorType("no such link found")
	// ErrArrayOutOfRange is returned when Resolve link is called on a list that where the index is out of range
	ErrArrayOutOfRange = errorType("array index out of range")
	// ErrNoLinks is returned when we try to traverse through a terminal
	ErrNoLinks = errorType("tried to resolve through object that had no links")
)

// LegacyNode wraps a go-ipld-prime node & a block so that it can be treated as
// a legacy ipld format node
type LegacyNode struct {
	blocks.Block
	ipld.Node
}

func (ln *LegacyNode) focusToLinkEdge(p ipld.Path) (ipld.Node, []ipld.PathSegment, error) {
	n := ln.Node
	for i, seg := range p.Segments() {
		switch n.Kind() {
		case ipld.Kind_Invalid:
			panic(fmt.Errorf("invalid node encountered at %q", p.Truncate(i)))
		case ipld.Kind_Map:
			next, err := n.LookupByString(seg.String())
			if err != nil {
				return nil, nil, ErrorAtPathSegment(seg, p.Truncate(i), ErrNoSuchLink)
			}
			n = next
		case ipld.Kind_List:
			intSeg, err := seg.Index()
			if err != nil {
				return nil, nil, ErrorAtPathSegment(seg, p.Truncate(i), errors.New("the segment cannot be parsed as a number and the node is a list"))
			}
			next, err := n.LookupByIndex(intSeg)
			if err != nil {
				return nil, nil, ErrorAtPathSegment(seg, p.Truncate(i), ErrArrayOutOfRange)
			}
			n = next
		case ipld.Kind_Link:
			return n, p.Segments()[i:], nil
		default:
			return nil, nil, ErrorAtPath(p.Truncate(i), ErrNoLinks)
		}
	}
	return n, nil, nil
}

// Resolve resolves a path through this node, stopping at any link boundary
// and returning the object found as well as the remaining path to traverse
func (ln *LegacyNode) Resolve(path []string) (interface{}, []string, error) {
	segments := make([]ipld.PathSegment, 0, len(path))
	for _, pathSegment := range path {
		segments = append(segments, ipld.ParsePathSegment(pathSegment))
	}
	p := ipld.NewPath(segments)
	n, remaining, err := ln.focusToLinkEdge(p)
	if err != nil {
		return nil, nil, err
	}
	var remainingStrings []string
	if len(remaining) > 0 {
		remainingStrings = make([]string, 0, len(remaining))
		for _, segment := range remaining {
			remainingStrings = append(remainingStrings, segment.String())
		}
	}
	if n.Kind() == ipld.Kind_Link {
		link, _ := n.AsLink()
		return &format.Link{Cid: link.(cidlink.Link).Cid}, remainingStrings, nil
	}
	buf := new(bytes.Buffer)
	err = dagjson.Encode(n, buf)
	if err != nil {
		return nil, nil, err
	}
	var out interface{}
	err = json.Unmarshal(buf.Bytes(), &out)
	if err != nil {
		return nil, nil, err
	}
	return out, remainingStrings, nil
}

func pathsToDepth(n ipld.Node, pathPrefix string, depth int) []string {
	if depth == 0 {
		return nil
	}
	switch n.Kind() {
	case ipld.Kind_Invalid:
		panic(fmt.Errorf("invalid node encountered at %q", pathPrefix))
	case ipld.Kind_Map:
		iter := n.MapIterator()
		var paths []string
		for !iter.Done() {
			k, v, err := iter.Next()
			if err != nil {
				return nil
			}
			ks, err := k.AsString()
			if err != nil {
				return nil
			}
			newPath := pathPrefix + ks
			paths = append(paths, newPath)
			paths = append(paths, pathsToDepth(v, newPath+"/", depth-1)...)
		}
		return paths
	case ipld.Kind_List:
		iter := n.ListIterator()
		var paths []string
		for !iter.Done() {
			idx, v, err := iter.Next()
			if err != nil {
				return nil
			}
			newPath := pathPrefix + strconv.Itoa(int(idx))
			paths = append(paths, newPath)
			paths = append(paths, pathsToDepth(v, newPath+"/", depth-1)...)
		}
		return paths
	default:
		return nil
	}
}

// Tree lists all paths within the object under 'path', and up to the given depth.
// To list the entire object (similar to `find .`) pass "" and -1
func (ln *LegacyNode) Tree(path string, depth int) []string {
	var n ipld.Node = ln
	if path != "" {
		p := ipld.ParsePath(path)
		focused, remaining, err := ln.focusToLinkEdge(p)
		if err != nil {
			return nil
		}
		if len(remaining) > 0 {
			return nil
		}
		n = focused
	}
	return pathsToDepth(n, "", depth)
}

// ResolveLink is a helper function that calls resolve and asserts the
// output is a link
func (ln *LegacyNode) ResolveLink(path []string) (*format.Link, []string, error) {
	obj, rest, err := ln.Resolve(path)
	if err != nil {
		return nil, nil, err
	}
	lnk, ok := obj.(*format.Link)
	if !ok {
		return nil, rest, ErrNonLink
	}

	return lnk, rest, nil
}

// Copy returns a deep copy of this node
func (ln *LegacyNode) Copy() format.Node {
	nb := ln.Node.Prototype().NewBuilder()
	err := nb.AssignNode(ln.Node)
	if err != nil {
		return nil
	}
	return &LegacyNode{ln.Block, nb.Build()}
}

// Links is a helper function that returns all links within this object
func (ln *LegacyNode) Links() []*format.Link {
	links, _ := traversal.SelectLinks(ln.Node)
	if len(links) == 0 {
		return nil
	}
	legacyLinks := make([]*format.Link, 0, len(links))
	for _, link := range links {
		legacyLinks = append(legacyLinks, &format.Link{Cid: link.(cidlink.Link).Cid})
	}
	return legacyLinks
}

// TODO: not sure if stat deserves to stay
func (ln *LegacyNode) Stat() (*format.NodeStat, error) {
	return &format.NodeStat{}, nil
}

// Size returns the size in bytes of the serialized object
func (ln *LegacyNode) Size() (uint64, error) {
	return uint64(len(ln.RawData())), nil
}

var _ UniversalNode = &LegacyNode{}
