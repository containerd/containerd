// go-ipld-prime is a series of go interfaces for manipulating IPLD data.
//
// See https://github.com/ipld/specs for more information about the basics
// of "What is IPLD?".
//
// See https://github.com/ipld/go-ipld-prime/tree/master/doc/README.md
// for more documentation about go-ipld-prime's architecture and usage.
//
// Here in the godoc, the first couple of types to look at should be:
//
//   - Node
//   - NodeBuilder and NodeAssembler
//   - NodePrototype.
//
// These types provide a generic description of the data model.
//
// A Node is a piece of IPLD data which can be inspected.
// A NodeAssembler is used to create Nodes.
// (A NodeBuilder is just like a NodeAssembler, but allocates memory
// (whereas a NodeAssembler just fills up memory; using these carefully
// allows construction of very efficient code.)
//
// Different NodePrototypes can be used to describe Nodes which follow certain logical rules
// (e.g., we use these as part of implementing Schemas),
// and can also be used so that programs can use different memory layouts for different data
// (which can be useful for constructing efficient programs when data has known shape for
// which we can use specific or compacted memory layouts).
//
// If working with linked data (data which is split into multiple
// trees of Nodes, loaded separately, and connected by some kind of
// "link" reference), the next types you should look at are:
//
//   - LinkSystem
//   - ... and its fields.
//
// The most typical use of LinkSystem is to use the linking/cid package
// to get a LinkSystem that works with CIDs:
//
//   lsys := cidlink.DefaultLinkSystem()
//
// ... and then assign the StorageWriteOpener and StorageReadOpener fields
// in order to control where data is stored to and read from.
// Methods on the LinkSystem then provide the functions typically used
// to get data in and out of Nodes so you can work with it.
//
// This root package only provides the essential interfaces,
// as well as a Path implementation, and a variety of error types.
// Most actual functionality is found in subpackages.
//
// Particularly interesting subpackages include:
//
//   - node/* -- various Node + NodeBuilder implementations
//   - node/basic -- the first Node implementation you should try
//   - codec/* -- functions for serializing and deserializing Nodes
//   - linking/* -- various Link + LinkBuilder implementations
//   - traversal -- functions for walking Node graphs (including
//        automatic link loading) and visiting
//   - must -- helpful functions for streamlining error handling
//   - fluent -- alternative Node interfaces that flip errors to panics
//   - schema -- interfaces for working with IPLD Schemas and Nodes
//        which use Schema types and constraints
//
// Note that since interfaces in this package are the core of the library,
// choices made here maximize correctness and performance -- these choices
// are *not* always the choices that would maximize ergonomics.
// (Ergonomics can come on top; performance generally can't.)
// You can check out the 'must' or 'fluent' packages for more ergonomics;
// 'traversal' provides some ergnomics features for certain uses;
// any use of schemas with codegen tooling will provide more ergnomic options;
// or you can make your own function decorators that do what *you* need.
//
package ipld
