package schema

import (
	"github.com/ipld/go-ipld-prime"
)

// schema.TypedNode is a superset of the ipld.Node interface, and has additional behaviors.
//
// A schema.TypedNode can be inspected for its schema.Type and schema.TypeKind,
// which conveys much more and richer information than the Data Model layer
// ipld.Kind.
//
// There are many different implementations of schema.TypedNode.
// One implementation can wrap any other existing ipld.Node (i.e., it's zero-copy)
// and promises that it has *already* been validated to match the typesystem.Type;
// another implementation similarly wraps any other existing ipld.Node, but
// defers to the typesystem validation checking to fields that are accessed;
// and when using code generation tools, all of the generated native Golang
// types produced by the codegen will each individually implement schema.TypedNode.
//
// Typed nodes sometimes have slightly different behaviors than plain nodes:
// For example, when looking up fields on a typed node that's a struct,
// the error returned for a lookup with a key that's not a field name will
// be ErrNoSuchField (instead of ErrNotExists).
// These behaviors apply to the schema.TypedNode only and not their representations;
// continuing the example, the .Representation().LookupByString() method on
// that same node for the same key as plain `.LookupByString()` will still
// return ErrNotExists, because the representation isn't a schema.TypedNode!
type TypedNode interface {
	// schema.TypedNode acts just like a regular Node for almost all purposes;
	// which ipld.Kind it acts as is determined by the TypeKind.
	// (Note that the representation strategy of the type does *not* affect
	// the Kind of schema.TypedNode -- rather, the representation strategy
	// affects the `.Representation().Kind()`.)
	//
	// For example: if the `.Type().TypeKind()` of this node is "struct",
	// it will act like Kind() == "map"
	// (even if Type().(Struct).ReprStrategy() is "tuple").
	ipld.Node

	// Type returns a reference to the reified schema.Type value.
	Type() Type

	// Representation returns an ipld.Node which sees the data in this node
	// in its representation form.
	//
	// For example: if the `.Type().TypeKind()` of this node is "struct",
	// `.Representation().TypeKind()` may vary based on its representation strategy:
	// if the representation strategy is "map", then it will be Kind=="map";
	// if the streatgy is "tuple", then it will be Kind=="list".
	Representation() ipld.Node
}

// schema.TypedLinkNode is a superset of the schema.TypedNode interface, and has one additional behavior.
//
// A schema.TypedLinkNode contains a hint for the appropriate node builder to use for loading data
// on the other side of the link contained within the node, so that it can be assembled
// into a node representation and validated against the schema as quickly as possible
//
// So, for example, if you wanted to support loading the other side of a link
// with a code-gen'd node builder while utilizing the automatic loading facilities
// of the traversal package, you could write a LinkNodeBuilderChooser as follows:
//
//		func LinkNodeBuilderChooser(lnk ipld.Link, lnkCtx ipld.LinkContext) ipld.NodePrototype {
//			if tlnkNd, ok := lnkCtx.LinkNode.(schema.TypedLinkNode); ok {
//				return tlnkNd.LinkTargetNodePrototype()
//			}
//			return basicnode.Prototype__Any{}
//		}
//
type TypedLinkNode interface {
	LinkTargetNodePrototype() ipld.NodePrototype
}

// TypedPrototype is a superset of the ipld.Nodeprototype interface, and has
// additional behaviors, much like TypedNode for ipld.Node.
type TypedPrototype interface {
	ipld.NodePrototype

	// Type returns a reference to the reified schema.Type value.
	Type() Type

	// Representation returns an ipld.NodePrototype for the representation
	// form of the prototype.
	Representation() ipld.NodePrototype
}
