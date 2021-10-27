package cbornode

import (
	"encoding/json"
	"errors"
	"io"
	"math"
	"strconv"
	"strings"

	blocks "github.com/ipfs/go-block-format"
	cid "github.com/ipfs/go-cid"
	node "github.com/ipfs/go-ipld-format"
	mh "github.com/multiformats/go-multihash"
)

// CBORTagLink is the integer used to represent tags in CBOR.
const CBORTagLink = 42

// Node represents an IPLD node.
type Node struct {
	obj   interface{}
	tree  []string
	links []*node.Link
	raw   []byte
	cid   cid.Cid
}

// Compile time check to make sure Node implements the node.Node interface
var _ node.Node = (*Node)(nil)

var (
	// ErrNoSuchLink is returned when no link with the given name was found.
	ErrNoSuchLink       = errors.New("no such link found")
	ErrNonLink          = errors.New("non-link found at given path")
	ErrInvalidLink      = errors.New("link value should have been bytes")
	ErrInvalidKeys      = errors.New("map keys must be strings")
	ErrArrayOutOfRange  = errors.New("array index out of range")
	ErrNoLinks          = errors.New("tried to resolve through object that had no links")
	ErrEmptyLink        = errors.New("link value was empty")
	ErrInvalidMultibase = errors.New("invalid multibase on IPLD link")
	ErrNonStringLink    = errors.New("link should have been a string")
)

// DecodeBlock decodes a CBOR encoded Block into an IPLD Node.
//
// This method *does not* canonicalize and *will* preserve the CID. As a matter
// of fact, it will assume that `block.Cid()` returns the correct CID and will
// make no effort to validate this assumption.
//
// In general, you should not be calling this method directly. Instead, you
// should be calling the `Decode` method from the `go-ipld-format` package. That
// method will pick the right decoder based on the Block's CID.
//
// Note: This function keeps a reference to `block` and assumes that it is
// immutable.
func DecodeBlock(block blocks.Block) (node.Node, error) {
	return decodeBlock(block)
}

func decodeBlock(block blocks.Block) (*Node, error) {
	var m interface{}
	if err := DecodeInto(block.RawData(), &m); err != nil {
		return nil, err
	}
	return newObject(block, m)
}

func newObject(block blocks.Block, m interface{}) (*Node, error) {
	tree, links, err := compute(m)
	if err != nil {
		return nil, err
	}

	return &Node{
		obj:   m,
		tree:  tree,
		links: links,
		raw:   block.RawData(),
		cid:   block.Cid(),
	}, nil
}

var _ node.DecodeBlockFunc = DecodeBlock

// Decode decodes a CBOR object into an IPLD Node.
//
// If passed a non-canonical CBOR node, this function will canonicalize it.
// Therefore, `bytes.Equal(b, Decode(b).RawData())` may not hold. If you already
// have a CID for this data and want to ensure that it doesn't change, you
// should use `DecodeBlock`.
// mhType is multihash code to use for hashing, for example mh.SHA2_256
//
// Note: This function does not hold onto `b`. You may reuse it.
func Decode(b []byte, mhType uint64, mhLen int) (*Node, error) {
	var m interface{}
	if err := DecodeInto(b, &m); err != nil {
		return nil, err
	}

	// We throw away `b` here to ensure that we canonicalize the encoded
	// CBOR object.
	return WrapObject(m, mhType, mhLen)
}

// DecodeInto decodes a serialized IPLD cbor object into the given object.
func DecodeInto(b []byte, v interface{}) error {
	return unmarshaller.Unmarshal(b, v)
}

func DecodeReader(r io.Reader, v interface{}) error {
	return unmarshaller.Decode(r, v)
}

// WrapObject converts an arbitrary object into a Node.
func WrapObject(m interface{}, mhType uint64, mhLen int) (*Node, error) {
	data, err := marshaller.Marshal(m)
	if err != nil {
		return nil, err
	}

	var obj interface{}
	err = cloner.Clone(m, &obj)
	if err != nil {
		return nil, err
	}

	if mhType == math.MaxUint64 {
		mhType = mh.SHA2_256
	}

	hash, err := mh.Sum(data, mhType, mhLen)
	if err != nil {
		return nil, err
	}
	c := cid.NewCidV1(cid.DagCBOR, hash)

	block, err := blocks.NewBlockWithCid(data, c)
	if err != nil {
		// TODO: Shouldn't this just panic?
		return nil, err
	}
	// No need to deserialize. We can just deep copy.
	return newObject(block, obj)
}

// Resolve resolves a given path, and returns the object found at the end, as well
// as the possible tail of the path that was not resolved.
func (n *Node) Resolve(path []string) (interface{}, []string, error) {
	var cur interface{} = n.obj
	for i, val := range path {
		switch curv := cur.(type) {
		case map[string]interface{}:
			next, ok := curv[val]
			if !ok {
				return nil, nil, ErrNoSuchLink
			}

			cur = next
		case map[interface{}]interface{}:
			next, ok := curv[val]
			if !ok {
				return nil, nil, ErrNoSuchLink
			}

			cur = next
		case []interface{}:
			n, err := strconv.Atoi(val)
			if err != nil {
				return nil, nil, err
			}

			if n < 0 || n >= len(curv) {
				return nil, nil, ErrArrayOutOfRange
			}

			cur = curv[n]
		case cid.Cid:
			return &node.Link{Cid: curv}, path[i:], nil
		default:
			return nil, nil, ErrNoLinks
		}
	}

	lnk, ok := cur.(cid.Cid)
	if ok {
		return &node.Link{Cid: lnk}, nil, nil
	}

	jsonish, err := convertToJSONIsh(cur)
	if err != nil {
		return nil, nil, err
	}

	return jsonish, nil, nil
}

// Copy creates a copy of the Node.
func (n *Node) Copy() node.Node {
	links := make([]*node.Link, len(n.links))
	copy(links, n.links)

	raw := make([]byte, len(n.raw))
	copy(raw, n.raw)

	tree := make([]string, len(n.tree))
	copy(tree, n.tree)

	return &Node{
		obj:   copyObj(n.obj),
		links: links,
		raw:   raw,
		tree:  tree,
		cid:   n.cid,
	}
}

func copyObj(i interface{}) interface{} {
	switch i := i.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{})
		for k, v := range i {
			out[k] = copyObj(v)
		}
		return out
	case map[interface{}]interface{}:
		out := make(map[interface{}]interface{})
		for k, v := range i {
			out[k] = copyObj(v)
		}
		return out
	case []interface{}:
		var out []interface{}
		for _, v := range i {
			out = append(out, copyObj(v))
		}
		return out
	default:
		// TODO: do not be lazy
		// being lazy for now
		// use caution
		return i
	}
}

// ResolveLink resolves a path and returns the raw Link at the end, as well as
// the possible tail of the path that was not resolved.
func (n *Node) ResolveLink(path []string) (*node.Link, []string, error) {
	obj, rest, err := n.Resolve(path)
	if err != nil {
		return nil, nil, err
	}

	lnk, ok := obj.(*node.Link)
	if !ok {
		return nil, rest, ErrNonLink
	}

	return lnk, rest, nil
}

// Tree returns a flattend array of paths at the given path for the given depth.
func (n *Node) Tree(path string, depth int) []string {
	if path == "" && depth == -1 {
		return n.tree
	}

	var out []string
	for _, t := range n.tree {
		if !strings.HasPrefix(t, path) {
			continue
		}

		sub := strings.TrimLeft(t[len(path):], "/")
		if sub == "" {
			continue
		}

		if depth < 0 {
			out = append(out, sub)
			continue
		}

		parts := strings.Split(sub, "/")
		if len(parts) <= depth {
			out = append(out, sub)
		}
	}
	return out
}

func compute(obj interface{}) (tree []string, links []*node.Link, err error) {
	err = traverse(obj, "", func(name string, val interface{}) error {
		if name != "" {
			tree = append(tree, name[1:])
		}
		if lnk, ok := val.(cid.Cid); ok {
			links = append(links, &node.Link{Cid: lnk})
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	return tree, links, nil
}

// Links lists all known links of the Node.
func (n *Node) Links() []*node.Link {
	return n.links
}

func traverse(obj interface{}, cur string, cb func(string, interface{}) error) error {
	if err := cb(cur, obj); err != nil {
		return err
	}

	switch obj := obj.(type) {
	case map[string]interface{}:
		for k, v := range obj {
			this := cur + "/" + k
			if err := traverse(v, this, cb); err != nil {
				return err
			}
		}
		return nil
	case map[interface{}]interface{}:
		for k, v := range obj {
			ks, ok := k.(string)
			if !ok {
				return errors.New("map key was not a string")
			}
			this := cur + "/" + ks
			if err := traverse(v, this, cb); err != nil {
				return err
			}
		}
		return nil
	case []interface{}:
		for i, v := range obj {
			this := cur + "/" + strconv.Itoa(i)
			if err := traverse(v, this, cb); err != nil {
				return err
			}
		}
		return nil
	default:
		return nil
	}
}

// RawData returns the raw bytes that represent the Node as serialized CBOR.
func (n *Node) RawData() []byte {
	return n.raw
}

// Cid returns the canonical Cid of the NOde.
func (n *Node) Cid() cid.Cid {
	return n.cid
}

// Loggable returns a loggable representation of the Node.
func (n *Node) Loggable() map[string]interface{} {
	return map[string]interface{}{
		"node_type": "cbor",
		"cid":       n.Cid(),
	}
}

// Size returns the size of the binary representation of the Node.
func (n *Node) Size() (uint64, error) {
	return uint64(len(n.RawData())), nil
}

// Stat returns stats about the Node.
// TODO: implement?
func (n *Node) Stat() (*node.NodeStat, error) {
	return &node.NodeStat{}, nil
}

// String returns the string representation of the CID of the Node.
func (n *Node) String() string {
	return n.Cid().String()
}

// MarshalJSON converts the Node into its JSON representation.
func (n *Node) MarshalJSON() ([]byte, error) {
	out, err := convertToJSONIsh(n.obj)
	if err != nil {
		return nil, err
	}

	return json.Marshal(out)
}

// DumpObject marshals any object into its CBOR serialized byte representation
// TODO: rename
func DumpObject(obj interface{}) (out []byte, err error) {
	return marshaller.Marshal(obj)
}

func toSaneMap(n map[interface{}]interface{}) (interface{}, error) {
	if lnk, ok := n["/"]; ok && len(n) == 1 {
		lnkb, ok := lnk.([]byte)
		if !ok {
			return nil, ErrInvalidLink
		}

		c, err := cid.Cast(lnkb)
		if err != nil {
			return nil, err
		}

		return map[string]interface{}{"/": c}, nil
	}
	out := make(map[string]interface{})
	for k, v := range n {
		ks, ok := k.(string)
		if !ok {
			return nil, ErrInvalidKeys
		}

		obj, err := convertToJSONIsh(v)
		if err != nil {
			return nil, err
		}

		out[ks] = obj
	}

	return out, nil
}

func convertToJSONIsh(v interface{}) (interface{}, error) {
	switch v := v.(type) {
	case map[interface{}]interface{}:
		return toSaneMap(v)
	case []interface{}:
		var out []interface{}
		if len(v) == 0 && v != nil {
			return []interface{}{}, nil
		}
		for _, i := range v {
			obj, err := convertToJSONIsh(i)
			if err != nil {
				return nil, err
			}

			out = append(out, obj)
		}
		return out, nil
	default:
		return v, nil
	}
}

// FromJSON converts incoming JSON into a Node.
func FromJSON(r io.Reader, mhType uint64, mhLen int) (*Node, error) {
	var m interface{}
	err := json.NewDecoder(r).Decode(&m)
	if err != nil {
		return nil, err
	}

	obj, err := convertToCborIshObj(m)
	if err != nil {
		return nil, err
	}

	return WrapObject(obj, mhType, mhLen)
}

func convertToCborIshObj(i interface{}) (interface{}, error) {
	switch v := i.(type) {
	case map[string]interface{}:
		if len(v) == 0 && v != nil {
			return v, nil
		}

		if lnk, ok := v["/"]; ok && len(v) == 1 {
			// special case for links
			vstr, ok := lnk.(string)
			if !ok {
				return nil, ErrNonStringLink
			}

			return cid.Decode(vstr)
		}

		for a, b := range v {
			val, err := convertToCborIshObj(b)
			if err != nil {
				return nil, err
			}

			v[a] = val
		}
		return v, nil
	case []interface{}:
		if len(v) == 0 && v != nil {
			return v, nil
		}

		var out []interface{}
		for _, o := range v {
			obj, err := convertToCborIshObj(o)
			if err != nil {
				return nil, err
			}

			out = append(out, obj)
		}

		return out, nil
	default:
		return v, nil
	}
}

func castBytesToCid(x []byte) (cid.Cid, error) {
	if len(x) == 0 {
		return cid.Cid{}, ErrEmptyLink
	}

	// TODO: manually doing multibase checking here since our deps don't
	// support binary multibase yet
	if x[0] != 0 {
		return cid.Cid{}, ErrInvalidMultibase
	}

	c, err := cid.Cast(x[1:])
	if err != nil {
		return cid.Cid{}, ErrInvalidLink
	}

	return c, nil
}

func castCidToBytes(link cid.Cid) ([]byte, error) {
	if !link.Defined() {
		return nil, ErrEmptyLink
	}
	return append([]byte{0}, link.Bytes()...), nil
}
