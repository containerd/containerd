package basicnode

// Prototype embeds a NodePrototype for every kind of Node implementation in this package.
// You can use it like this:
//
// 		basicnode.Prototype.Map.NewBuilder().BeginMap() //...
//
// and:
//
// 		basicnode.Prototype.String.NewBuilder().AssignString("x") // ...
//
// Most of the prototypes are for one particular Kind of node (e.g. string, int, etc);
// you can use the "Any" style if you want a builder that can accept any kind of data.
var Prototype prototype

type prototype struct {
	Any    Prototype__Any
	Map    Prototype__Map
	List   Prototype__List
	Bool   Prototype__Bool
	Int    Prototype__Int
	Float  Prototype__Float
	String Prototype__String
	Bytes  Prototype__Bytes
	Link   Prototype__Link
}
