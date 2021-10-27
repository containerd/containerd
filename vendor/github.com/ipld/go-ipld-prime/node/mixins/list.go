package mixins

import (
	ipld "github.com/ipld/go-ipld-prime"
)

// List can be embedded in a struct to provide all the methods that
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
type List struct {
	TypeName string
}

func (List) Kind() ipld.Kind {
	return ipld.Kind_List
}
func (x List) LookupByString(string) (ipld.Node, error) {
	return nil, ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "LookupByString", AppropriateKind: ipld.KindSet_JustMap, ActualKind: ipld.Kind_List}
}
func (x List) LookupByNode(key ipld.Node) (ipld.Node, error) {
	return nil, ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "LookupByNode", AppropriateKind: ipld.KindSet_JustMap, ActualKind: ipld.Kind_List}
}
func (List) MapIterator() ipld.MapIterator {
	return nil
}
func (List) IsAbsent() bool {
	return false
}
func (List) IsNull() bool {
	return false
}
func (x List) AsBool() (bool, error) {
	return false, ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AsBool", AppropriateKind: ipld.KindSet_JustBool, ActualKind: ipld.Kind_List}
}
func (x List) AsInt() (int64, error) {
	return 0, ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AsInt", AppropriateKind: ipld.KindSet_JustInt, ActualKind: ipld.Kind_List}
}
func (x List) AsFloat() (float64, error) {
	return 0, ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AsFloat", AppropriateKind: ipld.KindSet_JustFloat, ActualKind: ipld.Kind_List}
}
func (x List) AsString() (string, error) {
	return "", ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AsString", AppropriateKind: ipld.KindSet_JustString, ActualKind: ipld.Kind_List}
}
func (x List) AsBytes() ([]byte, error) {
	return nil, ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AsBytes", AppropriateKind: ipld.KindSet_JustBytes, ActualKind: ipld.Kind_List}
}
func (x List) AsLink() (ipld.Link, error) {
	return nil, ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AsLink", AppropriateKind: ipld.KindSet_JustLink, ActualKind: ipld.Kind_List}
}

// ListAssembler has similar purpose as List, but for (you guessed it)
// the NodeAssembler interface rather than the Node interface.
type ListAssembler struct {
	TypeName string
}

func (x ListAssembler) BeginMap(sizeHint int64) (ipld.MapAssembler, error) {
	return nil, ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "BeginMap", AppropriateKind: ipld.KindSet_JustMap, ActualKind: ipld.Kind_List}
}
func (x ListAssembler) AssignNull() error {
	return ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AssignNull", AppropriateKind: ipld.KindSet_JustNull, ActualKind: ipld.Kind_List}
}
func (x ListAssembler) AssignBool(bool) error {
	return ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AssignBool", AppropriateKind: ipld.KindSet_JustBool, ActualKind: ipld.Kind_List}
}
func (x ListAssembler) AssignInt(int64) error {
	return ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AssignInt", AppropriateKind: ipld.KindSet_JustInt, ActualKind: ipld.Kind_List}
}
func (x ListAssembler) AssignFloat(float64) error {
	return ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AssignFloat", AppropriateKind: ipld.KindSet_JustFloat, ActualKind: ipld.Kind_List}
}
func (x ListAssembler) AssignString(string) error {
	return ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AssignString", AppropriateKind: ipld.KindSet_JustString, ActualKind: ipld.Kind_List}
}
func (x ListAssembler) AssignBytes([]byte) error {
	return ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AssignBytes", AppropriateKind: ipld.KindSet_JustBytes, ActualKind: ipld.Kind_List}
}
func (x ListAssembler) AssignLink(ipld.Link) error {
	return ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AssignLink", AppropriateKind: ipld.KindSet_JustLink, ActualKind: ipld.Kind_List}
}
