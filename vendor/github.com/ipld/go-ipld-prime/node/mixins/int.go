package mixins

import (
	ipld "github.com/ipld/go-ipld-prime"
)

// Int can be embedded in a struct to provide all the methods that
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
type Int struct {
	TypeName string
}

func (Int) Kind() ipld.Kind {
	return ipld.Kind_Int
}
func (x Int) LookupByString(string) (ipld.Node, error) {
	return nil, ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "LookupByString", AppropriateKind: ipld.KindSet_JustMap, ActualKind: ipld.Kind_Int}
}
func (x Int) LookupByNode(key ipld.Node) (ipld.Node, error) {
	return nil, ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "LookupByNode", AppropriateKind: ipld.KindSet_JustMap, ActualKind: ipld.Kind_Int}
}
func (x Int) LookupByIndex(idx int64) (ipld.Node, error) {
	return nil, ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "LookupByIndex", AppropriateKind: ipld.KindSet_JustList, ActualKind: ipld.Kind_Int}
}
func (x Int) LookupBySegment(ipld.PathSegment) (ipld.Node, error) {
	return nil, ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "LookupBySegment", AppropriateKind: ipld.KindSet_Recursive, ActualKind: ipld.Kind_Int}
}
func (Int) MapIterator() ipld.MapIterator {
	return nil
}
func (Int) ListIterator() ipld.ListIterator {
	return nil
}
func (Int) Length() int64 {
	return -1
}
func (Int) IsAbsent() bool {
	return false
}
func (Int) IsNull() bool {
	return false
}
func (x Int) AsBool() (bool, error) {
	return false, ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AsBool", AppropriateKind: ipld.KindSet_JustBool, ActualKind: ipld.Kind_Int}
}
func (x Int) AsFloat() (float64, error) {
	return 0, ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AsFloat", AppropriateKind: ipld.KindSet_JustFloat, ActualKind: ipld.Kind_Int}
}
func (x Int) AsString() (string, error) {
	return "", ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AsString", AppropriateKind: ipld.KindSet_JustString, ActualKind: ipld.Kind_Int}
}
func (x Int) AsBytes() ([]byte, error) {
	return nil, ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AsBytes", AppropriateKind: ipld.KindSet_JustBytes, ActualKind: ipld.Kind_Int}
}
func (x Int) AsLink() (ipld.Link, error) {
	return nil, ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AsLink", AppropriateKind: ipld.KindSet_JustLink, ActualKind: ipld.Kind_Int}
}

// IntAssembler has similar purpose as Int, but for (you guessed it)
// the NodeAssembler interface rather than the Node interface.
type IntAssembler struct {
	TypeName string
}

func (x IntAssembler) BeginMap(sizeHint int64) (ipld.MapAssembler, error) {
	return nil, ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "BeginMap", AppropriateKind: ipld.KindSet_JustMap, ActualKind: ipld.Kind_Int}
}
func (x IntAssembler) BeginList(sizeHint int64) (ipld.ListAssembler, error) {
	return nil, ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "BeginList", AppropriateKind: ipld.KindSet_JustList, ActualKind: ipld.Kind_Int}
}
func (x IntAssembler) AssignNull() error {
	return ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AssignNull", AppropriateKind: ipld.KindSet_JustNull, ActualKind: ipld.Kind_Int}
}
func (x IntAssembler) AssignBool(bool) error {
	return ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AssignBool", AppropriateKind: ipld.KindSet_JustBool, ActualKind: ipld.Kind_Int}
}
func (x IntAssembler) AssignFloat(float64) error {
	return ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AssignFloat", AppropriateKind: ipld.KindSet_JustFloat, ActualKind: ipld.Kind_Int}
}
func (x IntAssembler) AssignString(string) error {
	return ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AssignString", AppropriateKind: ipld.KindSet_JustString, ActualKind: ipld.Kind_Int}
}
func (x IntAssembler) AssignBytes([]byte) error {
	return ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AssignBytes", AppropriateKind: ipld.KindSet_JustBytes, ActualKind: ipld.Kind_Int}
}
func (x IntAssembler) AssignLink(ipld.Link) error {
	return ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AssignLink", AppropriateKind: ipld.KindSet_JustLink, ActualKind: ipld.Kind_Int}
}
