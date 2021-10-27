package merkledag

import (
	"encoding/json"
	"fmt"

	blocks "github.com/ipfs/go-block-format"
	u "github.com/ipfs/go-ipfs-util"
	legacy "github.com/ipfs/go-ipld-legacy"
	ipld "github.com/ipld/go-ipld-prime"
	basicnode "github.com/ipld/go-ipld-prime/node/basic"

	cid "github.com/ipfs/go-cid"
	format "github.com/ipfs/go-ipld-format"
)

// RawNode represents a node which only contains data.
type RawNode struct {
	blocks.Block

	// Always a node/basic Bytes.
	// We can't reference a specific type, as it's not exposed there.
	// If we find that the interface indirection really matters,
	// then we could possibly use dagpb.Bytes.
	ipld.Node
}

var _ legacy.UniversalNode = &RawNode{}

// NewRawNode creates a RawNode using the default sha2-256 hash function.
func NewRawNode(data []byte) *RawNode {
	h := u.Hash(data)
	c := cid.NewCidV1(cid.Raw, h)
	blk, _ := blocks.NewBlockWithCid(data, c)
	return &RawNode{blk, basicnode.NewBytes(data)}
}

// DecodeRawBlock is a block decoder for raw IPLD nodes conforming to `node.DecodeBlockFunc`.
func DecodeRawBlock(block blocks.Block) (format.Node, error) {
	if block.Cid().Type() != cid.Raw {
		return nil, fmt.Errorf("raw nodes cannot be decoded from non-raw blocks: %d", block.Cid().Type())
	}
	// Once you "share" a block, it should be immutable. Therefore, we can just use this block as-is.
	return &RawNode{block, basicnode.NewBytes(block.RawData())}, nil
}

var _ format.DecodeBlockFunc = DecodeRawBlock

// NewRawNodeWPrefix creates a RawNode using the provided cid builder
func NewRawNodeWPrefix(data []byte, builder cid.Builder) (*RawNode, error) {
	builder = builder.WithCodec(cid.Raw)
	c, err := builder.Sum(data)
	if err != nil {
		return nil, err
	}
	blk, err := blocks.NewBlockWithCid(data, c)
	if err != nil {
		return nil, err
	}
	// Once you "share" a block, it should be immutable. Therefore, we can just use this block as-is.
	return &RawNode{blk, basicnode.NewBytes(data)}, nil
}

// Links returns nil.
func (rn *RawNode) Links() []*format.Link {
	return nil
}

// ResolveLink returns an error.
func (rn *RawNode) ResolveLink(path []string) (*format.Link, []string, error) {
	return nil, nil, ErrLinkNotFound
}

// Resolve returns an error.
func (rn *RawNode) Resolve(path []string) (interface{}, []string, error) {
	return nil, nil, ErrLinkNotFound
}

// Tree returns nil.
func (rn *RawNode) Tree(p string, depth int) []string {
	return nil
}

// Copy performs a deep copy of this node and returns it as an format.Node
func (rn *RawNode) Copy() format.Node {
	copybuf := make([]byte, len(rn.RawData()))
	copy(copybuf, rn.RawData())
	nblk, err := blocks.NewBlockWithCid(rn.RawData(), rn.Cid())
	if err != nil {
		// programmer error
		panic("failure attempting to clone raw block: " + err.Error())
	}
	// Once you "share" a block, it should be immutable. Therefore, we can just use this block as-is.
	return &RawNode{nblk, basicnode.NewBytes(nblk.RawData())}
}

// Size returns the size of this node
func (rn *RawNode) Size() (uint64, error) {
	return uint64(len(rn.RawData())), nil
}

// Stat returns some Stats about this node.
func (rn *RawNode) Stat() (*format.NodeStat, error) {
	return &format.NodeStat{
		CumulativeSize: len(rn.RawData()),
		DataSize:       len(rn.RawData()),
	}, nil
}

// MarshalJSON is required for our "ipfs dag" commands.
func (rn *RawNode) MarshalJSON() ([]byte, error) {
	return json.Marshal(string(rn.RawData()))
}

func RawNodeConverter(b blocks.Block, nd ipld.Node) (legacy.UniversalNode, error) {
	if nd.Kind() != ipld.Kind_Bytes {
		return nil, ErrNotRawNode
	}
	return &RawNode{b, nd}, nil
}

var _ legacy.UniversalNode = (*RawNode)(nil)
