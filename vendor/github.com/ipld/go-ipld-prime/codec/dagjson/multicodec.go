package dagjson

import (
	"io"

	"github.com/ipld/go-ipld-prime"
	"github.com/ipld/go-ipld-prime/codec"
	"github.com/ipld/go-ipld-prime/multicodec"
)

var (
	_ ipld.Decoder = Decode
	_ ipld.Encoder = Encode
)

func init() {
	multicodec.RegisterEncoder(0x0129, Encode)
	multicodec.RegisterDecoder(0x0129, Decode)
}

// Decode deserializes data from the given io.Reader and feeds it into the given ipld.NodeAssembler.
// Decode fits the ipld.Decoder function interface.
//
// A similar function is available on DecodeOptions type if you would like to customize any of the decoding details.
// This function uses the defaults for the dag-json codec
// (meaning: links are decoded, and bytes are decoded).
//
// This is the function that will be registered in the default multicodec registry during package init time.
func Decode(na ipld.NodeAssembler, r io.Reader) error {
	return DecodeOptions{
		ParseLinks: true,
		ParseBytes: true,
	}.Decode(na, r)
}

// Encode walks the given ipld.Node and serializes it to the given io.Writer.
// Encode fits the ipld.Encoder function interface.
//
// A similar function is available on EncodeOptions type if you would like to customize any of the encoding details.
// This function uses the defaults for the dag-json codec
// (meaning: links are encoded, bytes are encoded, and map keys are sorted during encode).
//
// This is the function that will be registered in the default multicodec registry during package init time.
func Encode(n ipld.Node, w io.Writer) error {
	return EncodeOptions{
		EncodeLinks: true,
		EncodeBytes: true,
		MapSortMode: codec.MapSortMode_Lexical,
	}.Encode(n, w)
}
