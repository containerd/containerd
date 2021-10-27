package mixins

import (
	ipld "github.com/ipld/go-ipld-prime"
)

// Float can be embedded in a struct to provide all the methods that
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
type Float struct {
	TypeName string
}

func (Float) Kind() ipld.Kind {
	return ipld.Kind_Float
}
func (x Float) LookupByString(string) (ipld.Node, error) {
	return nil, ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "LookupByString", AppropriateKind: ipld.KindSet_JustMap, ActualKind: ipld.Kind_Float}
}
func (x Float) LookupByNode(key ipld.Node) (ipld.Node, error) {
	return nil, ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "LookupByNode", AppropriateKind: ipld.KindSet_JustMap, ActualKind: ipld.Kind_Float}
}
func (x Float) LookupByIndex(idx int64) (ipld.Node, error) {
	return nil, ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "LookupByIndex", AppropriateKind: ipld.KindSet_JustList, ActualKind: ipld.Kind_Float}
}
func (x Float) LookupBySegment(ipld.PathSegment) (ipld.Node, error) {
	return nil, ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "LookupBySegment", AppropriateKind: ipld.KindSet_Recursive, ActualKind: ipld.Kind_Float}
}
func (Float) MapIterator() ipld.MapIterator {
	return nil
}
func (Float) ListIterator() ipld.ListIterator {
	return nil
}
func (Float) Length() int64 {
	return -1
}
func (Float) IsAbsent() bool {
	return false
}
func (Float) IsNull() bool {
	return false
}
func (x Float) AsBool() (bool, error) {
	return false, ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AsBool", AppropriateKind: ipld.KindSet_JustBool, ActualKind: ipld.Kind_Float}
}
func (x Float) AsInt() (int64, error) {
	return 0, ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AsInt", AppropriateKind: ipld.KindSet_JustInt, ActualKind: ipld.Kind_Float}
}
func (x Float) AsString() (string, error) {
	return "", ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AsString", AppropriateKind: ipld.KindSet_JustString, ActualKind: ipld.Kind_Float}
}
func (x Float) AsBytes() ([]byte, error) {
	return nil, ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AsBytes", AppropriateKind: ipld.KindSet_JustBytes, ActualKind: ipld.Kind_Float}
}
func (x Float) AsLink() (ipld.Link, error) {
	return nil, ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AsLink", AppropriateKind: ipld.KindSet_JustLink, ActualKind: ipld.Kind_Float}
}

// FloatAssembler has similar purpose as Float, but for (you guessed it)
// the NodeAssembler interface rather than the Node interface.
type FloatAssembler struct {
	TypeName string
}

func (x FloatAssembler) BeginMap(sizeHint int64) (ipld.MapAssembler, error) {
	return nil, ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "BeginMap", AppropriateKind: ipld.KindSet_JustMap, ActualKind: ipld.Kind_Float}
}
func (x FloatAssembler) BeginList(sizeHint int64) (ipld.ListAssembler, error) {
	return nil, ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "BeginList", AppropriateKind: ipld.KindSet_JustList, ActualKind: ipld.Kind_Float}
}
func (x FloatAssembler) AssignNull() error {
	return ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AssignNull", AppropriateKind: ipld.KindSet_JustNull, ActualKind: ipld.Kind_Float}
}
func (x FloatAssembler) AssignBool(bool) error {
	return ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AssignBool", AppropriateKind: ipld.KindSet_JustBool, ActualKind: ipld.Kind_Float}
}
func (x FloatAssembler) AssignInt(int64) error {
	return ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AssignInt", AppropriateKind: ipld.KindSet_JustInt, ActualKind: ipld.Kind_Float}
}
func (x FloatAssembler) AssignString(string) error {
	return ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AssignString", AppropriateKind: ipld.KindSet_JustString, ActualKind: ipld.Kind_Float}
}
func (x FloatAssembler) AssignBytes([]byte) error {
	return ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AssignBytes", AppropriateKind: ipld.KindSet_JustBytes, ActualKind: ipld.Kind_Float}
}
func (x FloatAssembler) AssignLink(ipld.Link) error {
	return ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AssignLink", AppropriateKind: ipld.KindSet_JustLink, ActualKind: ipld.Kind_Float}
}
