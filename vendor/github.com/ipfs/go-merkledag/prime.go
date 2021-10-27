package merkledag

import (
	dagpb "github.com/ipld/go-codec-dagpb"
	"github.com/ipld/go-ipld-prime"
)

// Protonode was originally implemented as a go-ipld-format node, and included
// functionality that does not fit well into the model for go-ipld-prime, namely
// the ability ot modify the node in place.

// In order to support the go-ipld-prime interface, all of these prime methods
// serialize and rebuild the go-ipld-prime node as needed, so that it remains up
// to date with mutations made via the add/remove link methods

// Kind returns a value from the Kind enum describing what the
// essential serializable kind of this node is (map, list, integer, etc).
// Most other handling of a node requires first switching upon the kind.
func (n *ProtoNode) Kind() ipld.Kind {
	_, _ = n.EncodeProtobuf(false)
	return n.encoded.Kind()
}

// LookupByString looks up a child object in this node and returns it.
// The returned Node may be any of the Kind:
// a primitive (string, int64, etc), a map, a list, or a link.
//
// If the Kind of this Node is not Kind_Map, a nil node and an error
// will be returned.
//
// If the key does not exist, a nil node and an error will be returned.
func (n *ProtoNode) LookupByString(key string) (ipld.Node, error) {
	_, err := n.EncodeProtobuf(false)
	if err != nil {
		return nil, err
	}
	return n.encoded.LookupByString(key)
}

// LookupByNode is the equivalent of LookupByString, but takes a reified Node
// as a parameter instead of a plain string.
// This mechanism is useful if working with typed maps (if the key types
// have constraints, and you already have a reified `schema.TypedNode` value,
// using that value can save parsing and validation costs);
// and may simply be convenient if you already have a Node value in hand.
//
// (When writing generic functions over Node, a good rule of thumb is:
// when handling a map, check for `schema.TypedNode`, and in this case prefer
// the LookupByNode(Node) method; otherwise, favor LookupByString; typically
// implementations will have their fastest paths thusly.)
func (n *ProtoNode) LookupByNode(key ipld.Node) (ipld.Node, error) {
	_, err := n.EncodeProtobuf(false)
	if err != nil {
		return nil, err
	}
	return n.encoded.LookupByNode(key)
}

// LookupByIndex is the equivalent of LookupByString but for indexing into a list.
// As with LookupByString, the returned Node may be any of the Kind:
// a primitive (string, int64, etc), a map, a list, or a link.
//
// If the Kind of this Node is not Kind_List, a nil node and an error
// will be returned.
//
// If idx is out of range, a nil node and an error will be returned.
func (n *ProtoNode) LookupByIndex(idx int64) (ipld.Node, error) {
	_, err := n.EncodeProtobuf(false)
	if err != nil {
		return nil, err
	}
	return n.encoded.LookupByIndex(idx)
}

// LookupBySegment is will act as either LookupByString or LookupByIndex,
// whichever is contextually appropriate.
//
// Using LookupBySegment may imply an "atoi" conversion if used on a list node,
// or an "itoa" conversion if used on a map node.  If an "itoa" conversion
// takes place, it may error, and this method may return that error.
func (n *ProtoNode) LookupBySegment(seg ipld.PathSegment) (ipld.Node, error) {
	_, err := n.EncodeProtobuf(false)
	if err != nil {
		return nil, err
	}
	return n.encoded.LookupBySegment(seg)
}

// Note that when using codegenerated types, there may be a fifth variant
// of lookup method on maps: `Get($GeneratedTypeKey) $GeneratedTypeValue`!
// MapIterator returns an iterator which yields key-value pairs
// traversing the node.
// If the node kind is anything other than a map, nil will be returned.
//
// The iterator will yield every entry in the map; that is, it
// can be expected that itr.Next will be called node.Length times
// before itr.Done becomes true.
func (n *ProtoNode) MapIterator() ipld.MapIterator {
	_, _ = n.EncodeProtobuf(false)
	return n.encoded.MapIterator()
}

// ListIterator returns an iterator which yields key-value pairs
// traversing the node.
// If the node kind is anything other than a list, nil will be returned.
//
// The iterator will yield every entry in the list; that is, it
// can be expected that itr.Next will be called node.Length times
// before itr.Done becomes true.
func (n *ProtoNode) ListIterator() ipld.ListIterator {
	_, _ = n.EncodeProtobuf(false)
	return n.encoded.ListIterator()
}

// Length returns the length of a list, or the number of entries in a map,
// or -1 if the node is not of list nor map kind.
func (n *ProtoNode) Length() int64 {
	_, _ = n.EncodeProtobuf(false)
	return n.encoded.Length()
}

// Absent nodes are returned when traversing a struct field that is
// defined by a schema but unset in the data.  (Absent nodes are not
// possible otherwise; you'll only see them from `schema.TypedNode`.)
// The absent flag is necessary so iterating over structs can
// unambiguously make the distinction between values that are
// present-and-null versus values that are absent.
//
// Absent nodes respond to `Kind()` as `ipld.Kind_Null`,
// for lack of any better descriptive value; you should therefore
// always check IsAbsent rather than just a switch on kind
// when it may be important to handle absent values distinctly.
func (n *ProtoNode) IsAbsent() bool {
	_, _ = n.EncodeProtobuf(false)
	return n.encoded.IsAbsent()
}

func (n *ProtoNode) IsNull() bool {
	_, _ = n.EncodeProtobuf(false)
	return n.encoded.IsNull()
}

func (n *ProtoNode) AsBool() (bool, error) {
	_, err := n.EncodeProtobuf(false)
	if err != nil {
		return false, err
	}
	return n.encoded.AsBool()
}

func (n *ProtoNode) AsInt() (int64, error) {
	_, err := n.EncodeProtobuf(false)
	if err != nil {
		return 0, err
	}
	return n.encoded.AsInt()
}

func (n *ProtoNode) AsFloat() (float64, error) {
	_, err := n.EncodeProtobuf(false)
	if err != nil {
		return 0, err
	}
	return n.encoded.AsFloat()
}

func (n *ProtoNode) AsString() (string, error) {
	_, err := n.EncodeProtobuf(false)
	if err != nil {
		return "", err
	}
	return n.encoded.AsString()
}

func (n *ProtoNode) AsBytes() ([]byte, error) {
	_, err := n.EncodeProtobuf(false)
	if err != nil {
		return nil, err
	}
	return n.encoded.AsBytes()
}

func (n *ProtoNode) AsLink() (ipld.Link, error) {
	_, err := n.EncodeProtobuf(false)
	if err != nil {
		return nil, err
	}
	return n.encoded.AsLink()
}

// Prototype returns a NodePrototype which can describe some properties of this node's implementation,
// and also be used to get a NodeBuilder,
// which can be use to create new nodes with the same implementation as this one.
//
// For typed nodes, the NodePrototype will also implement schema.Type.
//
// For Advanced Data Layouts, the NodePrototype will encapsulate any additional
// parameters and configuration of the ADL, and will also (usually)
// implement NodePrototypeSupportingAmend.
//
// Calling this method should not cause an allocation.
func (n *ProtoNode) Prototype() ipld.NodePrototype {
	return dagpb.Type.PBNode
}

var _ ipld.Node = &ProtoNode{}
