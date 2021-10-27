package basicnode

import (
	ipld "github.com/ipld/go-ipld-prime"
)

var (
	//_ ipld.Node          = &anyNode{}
	_ ipld.NodePrototype = Prototype__Any{}
	_ ipld.NodeBuilder   = &anyBuilder{}
	//_ ipld.NodeAssembler = &anyAssembler{}
)

// Note that we don't use a "var _" declaration to assert that Chooser
// implements traversal.LinkTargetNodePrototypeChooser, to keep basicnode's
// dependencies fairly light.

// Chooser implements traversal.LinkTargetNodePrototypeChooser.
//
// It can be used directly when loading links into the "any" prototype,
// or with another chooser layer on top, such as:
//
//    prototypeChooser := dagpb.AddSupportToChooser(basicnode.Chooser)
func Chooser(_ ipld.Link, _ ipld.LinkContext) (ipld.NodePrototype, error) {
	return Prototype__Any{}, nil
}

// anyNode is a union meant for alloc amortization; see anyAssembler.
// Note that anyBuilder doesn't use anyNode, because it's not aiming to amortize anything.
//
// REVIEW: if there's any point in keeping this around.  It's here for completeness,
// but not currently used anywhere in package, and also not currently exported.
// type anyNode struct {
// 	kind ipld.Kind
//
// 	plainMap
// 	plainList
// 	plainBool
// 	plainInt
// 	plainFloat
// 	plainString
// 	plainBytes
// 	plainLink
// }

// -- Node interface methods -->

// Unimplemented at present -- see "REVIEW" comment on anyNode.

// -- NodePrototype -->

type Prototype__Any struct{}

func (Prototype__Any) NewBuilder() ipld.NodeBuilder {
	return &anyBuilder{}
}

// -- NodeBuilder -->

// anyBuilder is a builder for any kind of node.
//
// anyBuilder is a little unusual in its internal workings:
// unlike most builders, it doesn't embed the corresponding assembler,
// nor will it end up using anyNode,
// but instead embeds a builder for each of the kinds it might contain.
// This is because we want a more granular return at the end:
// if we used anyNode, and returned a pointer to just the relevant part of it,
// we'd have all the extra bytes of anyNode still reachable in GC terms
// for as long as that handle to the interior of it remains live.
type anyBuilder struct {
	// kind is set on first interaction, and used to select which builder to delegate 'Build' to!
	// As soon as it's been set to a value other than zero (being "Invalid"), all other Assign/Begin calls will fail since something is already in progress.
	// May also be set to the magic value '99', which means "i dunno, I'm just carrying another node of unknown prototype".
	kind ipld.Kind

	// Only one of the following ends up being used...
	//  but we don't know in advance which one, so all are embeded here.
	//   This uses excessive space, but amortizes allocations, and all will be
	//    freed as soon as the builder is done.
	// Builders are only used for recursives;
	//  scalars are simple enough we just do them directly.
	// 'scalarNode' may also hold another Node of unknown prototype (possibly not even from this package),
	//  in which case this is indicated by 'kind==99'.

	mapBuilder  plainMap__Builder
	listBuilder plainList__Builder
	scalarNode  ipld.Node
}

func (nb *anyBuilder) Reset() {
	*nb = anyBuilder{}
}

func (nb *anyBuilder) BeginMap(sizeHint int64) (ipld.MapAssembler, error) {
	if nb.kind != ipld.Kind_Invalid {
		panic("misuse")
	}
	nb.kind = ipld.Kind_Map
	nb.mapBuilder.w = &plainMap{}
	return nb.mapBuilder.BeginMap(sizeHint)
}
func (nb *anyBuilder) BeginList(sizeHint int64) (ipld.ListAssembler, error) {
	if nb.kind != ipld.Kind_Invalid {
		panic("misuse")
	}
	nb.kind = ipld.Kind_List
	nb.listBuilder.w = &plainList{}
	return nb.listBuilder.BeginList(sizeHint)
}
func (nb *anyBuilder) AssignNull() error {
	if nb.kind != ipld.Kind_Invalid {
		panic("misuse")
	}
	nb.kind = ipld.Kind_Null
	return nil
}
func (nb *anyBuilder) AssignBool(v bool) error {
	if nb.kind != ipld.Kind_Invalid {
		panic("misuse")
	}
	nb.kind = ipld.Kind_Bool
	nb.scalarNode = NewBool(v)
	return nil
}
func (nb *anyBuilder) AssignInt(v int64) error {
	if nb.kind != ipld.Kind_Invalid {
		panic("misuse")
	}
	nb.kind = ipld.Kind_Int
	nb.scalarNode = NewInt(v)
	return nil
}
func (nb *anyBuilder) AssignFloat(v float64) error {
	if nb.kind != ipld.Kind_Invalid {
		panic("misuse")
	}
	nb.kind = ipld.Kind_Float
	nb.scalarNode = NewFloat(v)
	return nil
}
func (nb *anyBuilder) AssignString(v string) error {
	if nb.kind != ipld.Kind_Invalid {
		panic("misuse")
	}
	nb.kind = ipld.Kind_String
	nb.scalarNode = NewString(v)
	return nil
}
func (nb *anyBuilder) AssignBytes(v []byte) error {
	if nb.kind != ipld.Kind_Invalid {
		panic("misuse")
	}
	nb.kind = ipld.Kind_Bytes
	nb.scalarNode = NewBytes(v)
	return nil
}
func (nb *anyBuilder) AssignLink(v ipld.Link) error {
	if nb.kind != ipld.Kind_Invalid {
		panic("misuse")
	}
	nb.kind = ipld.Kind_Link
	nb.scalarNode = NewLink(v)
	return nil
}
func (nb *anyBuilder) AssignNode(v ipld.Node) error {
	if nb.kind != ipld.Kind_Invalid {
		panic("misuse")
	}
	nb.kind = 99
	nb.scalarNode = v
	return nil
}
func (anyBuilder) Prototype() ipld.NodePrototype {
	return Prototype__Any{}
}

func (nb *anyBuilder) Build() ipld.Node {
	switch nb.kind {
	case ipld.Kind_Invalid:
		panic("misuse")
	case ipld.Kind_Map:
		return nb.mapBuilder.Build()
	case ipld.Kind_List:
		return nb.listBuilder.Build()
	case ipld.Kind_Null:
		return ipld.Null
	case ipld.Kind_Bool:
		return nb.scalarNode
	case ipld.Kind_Int:
		return nb.scalarNode
	case ipld.Kind_Float:
		return nb.scalarNode
	case ipld.Kind_String:
		return nb.scalarNode
	case ipld.Kind_Bytes:
		return nb.scalarNode
	case ipld.Kind_Link:
		return nb.scalarNode
	case 99:
		return nb.scalarNode
	default:
		panic("unreachable")
	}
}

// -- NodeAssembler -->

// ... oddly enough, we seem to be able to put off implementing this
//  until we also implement something that goes full-hog on amortization
//   and actually has a slab of `anyNode`.  Which so far, nothing does.
//    See "REVIEW" comment on anyNode.
// type anyAssembler struct {
// 	w *anyNode
// }
