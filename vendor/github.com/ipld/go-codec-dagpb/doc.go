/*
Package dagpb provides an implementation of the IPLD DAG-PB spec
(https://github.com/ipld/specs/blob/master/block-layer/codecs/dag-pb.md) for
go-ipld-prime (https://github.com/ipld/go-ipld-prime/).

Use Decode() and Encode() directly, or import this package to have this codec
registered into the go-ipld-prime multicodec registry and available from the
cidlink.DefaultLinkSystem.

Nodes encoded with this codec _must_ conform to the DAG-PB spec. Specifically,
they should have the non-optional fields shown in the DAG-PB schema:

	type PBNode struct {
		Links [PBLink]
		Data optional Bytes
	}

	type PBLink struct {
		Hash Link
		Name optional String
		Tsize optional Int
	}

Use dagpb.Type.PBNode and friends directly for strictness guarantees. Basic
ipld.Node's will need to have the appropriate fields (and no others) to
successfully encode using this codec.
*/
package dagpb

//go:generate go run gen.go
//go:generate gofmt -w ipldsch_minima.go ipldsch_satisfaction.go ipldsch_types.go
