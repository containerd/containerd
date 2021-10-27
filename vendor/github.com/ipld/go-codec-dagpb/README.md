# go-dagpb

**An implementation of the IPLD [DAG-PB](https://github.com/ipld/specs/blob/master/block-layer/codecs/dag-pb.md) spec for [go-ipld-prime](https://github.com/ipld/go-ipld-prime/)**

Use `Decode(ipld.NodeAssembler, io.Reader)` and `Encode(ipld.Node, io.Writer)` directly, or import this package to have this codec registered into the go-ipld-prime CID link loader.

Nodes encoded with this codec _must_ conform to the DAG-PB spec. Specifically, they should have the non-optional fields shown in the DAG-PB schema:

```ipldsch
type PBNode struct {
  Links [PBLink]
  Data optional Bytes
}

type PBLink struct {
  Hash Link
  Name optional String
  Tsize optional Int
}
```

Use `dagpb.Type.PBNode` and friends directly for strictness guarantees. Basic `ipld.Node`s will need to have the appropraite fields (and no others) to successfully encode using this codec.

## License & Copyright

Copyright &copy; 2020 Protocol Labs

Licensed under either of

 * Apache 2.0, ([LICENSE-APACHE](LICENSE-APACHE) / http://www.apache.org/licenses/LICENSE-2.0)
 * MIT ([LICENSE-MIT](LICENSE-MIT) / http://opensource.org/licenses/MIT)

### Contribution

Unless you explicitly state otherwise, any contribution intentionally submitted for inclusion in the work by you, as defined in the Apache-2.0 license, shall be dual licensed as above, without any additional terms or conditions.
