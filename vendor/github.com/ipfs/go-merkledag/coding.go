package merkledag

import (
	"fmt"
	"sort"
	"strings"

	blocks "github.com/ipfs/go-block-format"
	cid "github.com/ipfs/go-cid"
	format "github.com/ipfs/go-ipld-format"
	pb "github.com/ipfs/go-merkledag/pb"
	dagpb "github.com/ipld/go-codec-dagpb"
	ipld "github.com/ipld/go-ipld-prime"
	"github.com/ipld/go-ipld-prime/fluent/qp"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
)

// Make sure the user doesn't upgrade this file.
// We need to check *here* as well as inside the `pb` package *just* in case the
// user replaces *all* go files in that package.
const _ = pb.DoNotUpgradeFileEverItWillChangeYourHashes

// for now, we use a PBNode intermediate thing.
// because native go objects are nice.

// unmarshal decodes raw data into a *Node instance.
// The conversion uses an intermediate PBNode.
func unmarshal(encodedBytes []byte) (*ProtoNode, error) {
	nb := dagpb.Type.PBNode.NewBuilder()
	if err := dagpb.DecodeBytes(nb, encodedBytes); err != nil {
		return nil, err
	}
	nd := nb.Build()
	return fromImmutableNode(&immutableProtoNode{encodedBytes, nd.(dagpb.PBNode)}), nil
}

func fromImmutableNode(encoded *immutableProtoNode) *ProtoNode {
	n := new(ProtoNode)
	n.encoded = encoded
	if n.encoded.PBNode.Data.Exists() {
		n.data = n.encoded.PBNode.Data.Must().Bytes()
	}
	numLinks := n.encoded.PBNode.Links.Length()
	n.links = make([]*format.Link, numLinks)
	linkAllocs := make([]format.Link, numLinks)
	for i := int64(0); i < numLinks; i++ {
		next := n.encoded.PBNode.Links.Lookup(i)
		name := ""
		if next.FieldName().Exists() {
			name = next.FieldName().Must().String()
		}
		c := cid.Undef
		c = next.FieldHash().Link().(cidlink.Link).Cid
		size := uint64(0)
		if next.FieldTsize().Exists() {
			size = uint64(next.FieldTsize().Must().Int())
		}
		link := &linkAllocs[i]
		link.Name = name
		link.Size = size
		link.Cid = c
		n.links[i] = link
	}
	return n
}
func (n *ProtoNode) marshalImmutable() (*immutableProtoNode, error) {
	nd, err := qp.BuildMap(dagpb.Type.PBNode, 2, func(ma ipld.MapAssembler) {
		qp.MapEntry(ma, "Links", qp.List(int64(len(n.links)), func(la ipld.ListAssembler) {
			for _, link := range n.links {
				qp.ListEntry(la, qp.Map(3, func(ma ipld.MapAssembler) {
					if link.Cid.Defined() {
						qp.MapEntry(ma, "Hash", qp.Link(cidlink.Link{Cid: link.Cid}))
					}
					qp.MapEntry(ma, "Name", qp.String(link.Name))
					qp.MapEntry(ma, "Tsize", qp.Int(int64(link.Size)))
				}))
			}
		}))
		if n.data != nil {
			qp.MapEntry(ma, "Data", qp.Bytes(n.data))
		}
	})
	if err != nil {
		return nil, err
	}

	// 1KiB can be allocated on the stack, and covers most small nodes
	// without having to grow the buffer and cause allocations.
	enc := make([]byte, 0, 1024)

	enc, err = dagpb.AppendEncode(enc, nd)
	if err != nil {
		return nil, err
	}
	return &immutableProtoNode{enc, nd.(dagpb.PBNode)}, nil
}

// Marshal encodes a *Node instance into a new byte slice.
// The conversion uses an intermediate PBNode.
func (n *ProtoNode) Marshal() ([]byte, error) {
	enc, err := n.marshalImmutable()
	if err != nil {
		return nil, err
	}
	return enc.encoded, nil
}

// GetPBNode converts *ProtoNode into it's protocol buffer variant.
// If you plan on mutating the data of the original node, it is recommended
// that you call ProtoNode.Copy() before calling ProtoNode.GetPBNode()
func (n *ProtoNode) GetPBNode() *pb.PBNode {
	pbn := &pb.PBNode{}
	if len(n.links) > 0 {
		pbn.Links = make([]*pb.PBLink, len(n.links))
	}

	sort.Stable(LinkSlice(n.links)) // keep links sorted
	for i, l := range n.links {
		pbn.Links[i] = &pb.PBLink{}
		pbn.Links[i].Name = &l.Name
		pbn.Links[i].Tsize = &l.Size
		if l.Cid.Defined() {
			pbn.Links[i].Hash = l.Cid.Bytes()
		}
	}

	if len(n.data) > 0 {
		pbn.Data = n.data
	}
	return pbn
}

// EncodeProtobuf returns the encoded raw data version of a Node instance.
// It may use a cached encoded version, unless the force flag is given.
func (n *ProtoNode) EncodeProtobuf(force bool) ([]byte, error) {
	sort.Stable(LinkSlice(n.links)) // keep links sorted
	if n.encoded == nil || force {
		n.cached = cid.Undef
		var err error
		n.encoded, err = n.marshalImmutable()
		if err != nil {
			return nil, err
		}
	}

	if !n.cached.Defined() {
		c, err := n.CidBuilder().Sum(n.encoded.encoded)
		if err != nil {
			return nil, err
		}

		n.cached = c
	}

	return n.encoded.encoded, nil
}

// DecodeProtobuf decodes raw data and returns a new Node instance.
func DecodeProtobuf(encoded []byte) (*ProtoNode, error) {
	n, err := unmarshal(encoded)
	if err != nil {
		return nil, fmt.Errorf("incorrectly formatted merkledag node: %s", err)
	}
	return n, nil
}

// DecodeProtobufBlock is a block decoder for protobuf IPLD nodes conforming to
// node.DecodeBlockFunc
func DecodeProtobufBlock(b blocks.Block) (format.Node, error) {
	c := b.Cid()
	if c.Type() != cid.DagProtobuf {
		return nil, fmt.Errorf("this function can only decode protobuf nodes")
	}

	decnd, err := DecodeProtobuf(b.RawData())
	if err != nil {
		if strings.Contains(err.Error(), "Unmarshal failed") {
			return nil, fmt.Errorf("the block referred to by '%s' was not a valid merkledag node", c)
		}
		return nil, fmt.Errorf("failed to decode Protocol Buffers: %v", err)
	}

	decnd.cached = c
	decnd.builder = c.Prefix()
	return decnd, nil
}

// Type assertion
var _ format.DecodeBlockFunc = DecodeProtobufBlock
