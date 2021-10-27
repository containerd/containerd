package mixins

import (
	ipld "github.com/ipld/go-ipld-prime"
)

// Bytes can be embedded in a struct to provide all the methods that
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
type Bytes struct {
	TypeName string
}

func (Bytes) Kind() ipld.Kind {
	return ipld.Kind_Bytes
}
func (x Bytes) LookupByString(string) (ipld.Node, error) {
	return nil, ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "LookupByString", AppropriateKind: ipld.KindSet_JustMap, ActualKind: ipld.Kind_Bytes}
}
func (x Bytes) LookupByNode(key ipld.Node) (ipld.Node, error) {
	return nil, ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "LookupByNode", AppropriateKind: ipld.KindSet_JustMap, ActualKind: ipld.Kind_Bytes}
}
func (x Bytes) LookupByIndex(idx int64) (ipld.Node, error) {
	return nil, ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "LookupByIndex", AppropriateKind: ipld.KindSet_JustList, ActualKind: ipld.Kind_Bytes}
}
func (x Bytes) LookupBySegment(ipld.PathSegment) (ipld.Node, error) {
	return nil, ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "LookupBySegment", AppropriateKind: ipld.KindSet_Recursive, ActualKind: ipld.Kind_Bytes}
}
func (Bytes) MapIterator() ipld.MapIterator {
	return nil
}
func (Bytes) ListIterator() ipld.ListIterator {
	return nil
}
func (Bytes) Length() int64 {
	return -1
}
func (Bytes) IsAbsent() bool {
	return false
}
func (Bytes) IsNull() bool {
	return false
}
func (x Bytes) AsBool() (bool, error) {
	return false, ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AsBool", AppropriateKind: ipld.KindSet_JustBool, ActualKind: ipld.Kind_Bytes}
}
func (x Bytes) AsInt() (int64, error) {
	return 0, ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AsInt", AppropriateKind: ipld.KindSet_JustInt, ActualKind: ipld.Kind_Bytes}
}
func (x Bytes) AsFloat() (float64, error) {
	return 0, ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AsFloat", AppropriateKind: ipld.KindSet_JustFloat, ActualKind: ipld.Kind_Bytes}
}
func (x Bytes) AsString() (string, error) {
	return "", ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AsString", AppropriateKind: ipld.KindSet_JustString, ActualKind: ipld.Kind_Bytes}
}
func (x Bytes) AsLink() (ipld.Link, error) {
	return nil, ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AsLink", AppropriateKind: ipld.KindSet_JustLink, ActualKind: ipld.Kind_Bytes}
}

// BytesAssembler has similar purpose as Bytes, but for (you guessed it)
// the NodeAssembler interface rather than the Node interface.
type BytesAssembler struct {
	TypeName string
}

func (x BytesAssembler) BeginMap(sizeHint int64) (ipld.MapAssembler, error) {
	return nil, ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "BeginMap", AppropriateKind: ipld.KindSet_JustMap, ActualKind: ipld.Kind_Bytes}
}
func (x BytesAssembler) BeginList(sizeHint int64) (ipld.ListAssembler, error) {
	return nil, ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "BeginList", AppropriateKind: ipld.KindSet_JustList, ActualKind: ipld.Kind_Bytes}
}
func (x BytesAssembler) AssignNull() error {
	return ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AssignNull", AppropriateKind: ipld.KindSet_JustNull, ActualKind: ipld.Kind_Bytes}
}
func (x BytesAssembler) AssignBool(bool) error {
	return ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AssignBool", AppropriateKind: ipld.KindSet_JustBool, ActualKind: ipld.Kind_Bytes}
}
func (x BytesAssembler) AssignInt(int64) error {
	return ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AssignInt", AppropriateKind: ipld.KindSet_JustInt, ActualKind: ipld.Kind_Bytes}
}
func (x BytesAssembler) AssignFloat(float64) error {
	return ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AssignFloat", AppropriateKind: ipld.KindSet_JustFloat, ActualKind: ipld.Kind_Bytes}
}
func (x BytesAssembler) AssignString(string) error {
	return ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AssignString", AppropriateKind: ipld.KindSet_JustString, ActualKind: ipld.Kind_Bytes}
}
func (x BytesAssembler) AssignLink(ipld.Link) error {
	return ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AssignLink", AppropriateKind: ipld.KindSet_JustLink, ActualKind: ipld.Kind_Bytes}
}
