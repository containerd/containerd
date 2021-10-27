// Package raw implements IPLD's raw codec, which simply writes and reads a Node
// which can be represented as bytes.
//
// The codec can be used with any node which supports AsBytes and AssignBytes.
// In general, it only makes sense to use this codec on a plain "bytes" node
// such as github.com/ipld/go-ipld-prime/node/basic.Prototype.Bytes.
package raw

import (
	"fmt"
	"io"
	"io/ioutil"

	ipld "github.com/ipld/go-ipld-prime"
	"github.com/ipld/go-ipld-prime/multicodec"
)

// TODO(mvdan): make go-ipld-prime use go-multicodec soon
const rawMulticodec = 0x55

var (
	_ ipld.Decoder = Decode
	_ ipld.Encoder = Encode
)

func init() {
	multicodec.RegisterEncoder(rawMulticodec, Encode)
	multicodec.RegisterDecoder(rawMulticodec, Decode)
}

// Decode implements decoding of a node with the raw codec.
//
// Note that if r has a Bytes method, such as is the case with *bytes.Buffer, we
// will use those bytes directly to save having to allocate and copy them. The
// Node interface is defined as immutable, so it is assumed that its bytes won't
// be modified in-place. Similarly, we assume that the incoming buffer's bytes
// won't get modified in-place later.
//
// To disable the shortcut above, hide the Bytes method by wrapping the buffer
// with an io.Reader:
//
//     Decode([...], struct{io.Reader}{buf})
func Decode(am ipld.NodeAssembler, r io.Reader) error {
	var data []byte
	if buf, ok := r.(interface{ Bytes() []byte }); ok {
		data = buf.Bytes()
	} else {
		var err error
		data, err = ioutil.ReadAll(r)
		if err != nil {
			return fmt.Errorf("could not decode raw node: %v", err)
		}
	}
	return am.AssignBytes(data)
}

// Encode implements encoding of a node with the raw codec.
//
// Note that Encode won't copy the node's bytes as returned by AsBytes, but the
// call to Write will typically have to copy the bytes anyway.
func Encode(node ipld.Node, w io.Writer) error {
	data, err := node.AsBytes()
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}
