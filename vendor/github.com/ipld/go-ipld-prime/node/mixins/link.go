package mixins

import (
	ipld "github.com/ipld/go-ipld-prime"
)

// Link can be embedded in a struct to provide all the methods that
// have fixed output for any int-kinded nodes.
// (Mostly this includes all the methods which simply return ErrWrongKind.)
// Other methods will still need to be implemented to finish conforming to Node.
//
// To conserve memory and get a TypeName in errors without embedding,
// write methods on your type with a body that simply initializes this struct
// and immediately uses the relevant method;
// this is more verbose in source, but compiles to a tighter result:
// in memory, there's no embed; and in runtime, the calls will be inlined
// and thus have no cost in execution time.
type Link struct {
	TypeName string
}

func (Link) Kind() ipld.Kind {
	return ipld.Kind_Link
}
func (x Link) LookupByString(string) (ipld.Node, error) {
	return nil, ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "LookupByString", AppropriateKind: ipld.KindSet_JustMap, ActualKind: ipld.Kind_Link}
}
func (x Link) LookupByNode(key ipld.Node) (ipld.Node, error) {
	return nil, ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "LookupByNode", AppropriateKind: ipld.KindSet_JustMap, ActualKind: ipld.Kind_Link}
}
func (x Link) LookupByIndex(idx int64) (ipld.Node, error) {
	return nil, ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "LookupByIndex", AppropriateKind: ipld.KindSet_JustList, ActualKind: ipld.Kind_Link}
}
func (x Link) LookupBySegment(ipld.PathSegment) (ipld.Node, error) {
	return nil, ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "LookupBySegment", AppropriateKind: ipld.KindSet_Recursive, ActualKind: ipld.Kind_Link}
}
func (Link) MapIterator() ipld.MapIterator {
	return nil
}
func (Link) ListIterator() ipld.ListIterator {
	return nil
}
func (Link) Length() int64 {
	return -1
}
func (Link) IsAbsent() bool {
	return false
}
func (Link) IsNull() bool {
	return false
}
func (x Link) AsBool() (bool, error) {
	return false, ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AsBool", AppropriateKind: ipld.KindSet_JustBool, ActualKind: ipld.Kind_Link}
}
func (x Link) AsInt() (int64, error) {
	return 0, ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AsInt", AppropriateKind: ipld.KindSet_JustInt, ActualKind: ipld.Kind_Link}
}
func (x Link) AsFloat() (float64, error) {
	return 0, ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AsFloat", AppropriateKind: ipld.KindSet_JustFloat, ActualKind: ipld.Kind_Link}
}
func (x Link) AsString() (string, error) {
	return "", ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AsString", AppropriateKind: ipld.KindSet_JustString, ActualKind: ipld.Kind_Link}
}
func (x Link) AsBytes() ([]byte, error) {
	return nil, ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AsBytes", AppropriateKind: ipld.KindSet_JustBytes, ActualKind: ipld.Kind_Link}
}

// LinkAssembler has similar purpose as Link, but for (you guessed it)
// the NodeAssembler interface rather than the Node interface.
type LinkAssembler struct {
	TypeName string
}

func (x LinkAssembler) BeginMap(sizeHint int64) (ipld.MapAssembler, error) {
	return nil, ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "BeginMap", AppropriateKind: ipld.KindSet_JustMap, ActualKind: ipld.Kind_Link}
}
func (x LinkAssembler) BeginList(sizeHint int64) (ipld.ListAssembler, error) {
	return nil, ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "BeginList", AppropriateKind: ipld.KindSet_JustList, ActualKind: ipld.Kind_Link}
}
func (x LinkAssembler) AssignNull() error {
	return ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AssignNull", AppropriateKind: ipld.KindSet_JustNull, ActualKind: ipld.Kind_Link}
}
func (x LinkAssembler) AssignBool(bool) error {
	return ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AssignBool", AppropriateKind: ipld.KindSet_JustBool, ActualKind: ipld.Kind_Link}
}
func (x LinkAssembler) AssignInt(int64) error {
	return ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AssignInt", AppropriateKind: ipld.KindSet_JustInt, ActualKind: ipld.Kind_Link}
}
func (x LinkAssembler) AssignFloat(float64) error {
	return ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AssignFloat", AppropriateKind: ipld.KindSet_JustFloat, ActualKind: ipld.Kind_Link}
}
func (x LinkAssembler) AssignString(string) error {
	return ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AssignString", AppropriateKind: ipld.KindSet_JustString, ActualKind: ipld.Kind_Link}
}
func (x LinkAssembler) AssignBytes([]byte) error {
	return ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AssignBytes", AppropriateKind: ipld.KindSet_JustBytes, ActualKind: ipld.Kind_Link}
}
