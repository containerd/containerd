package mixins

import (
	ipld "github.com/ipld/go-ipld-prime"
)

// Bool can be embedded in a struct to provide all the methods that
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
type Bool struct {
	TypeName string
}

func (Bool) Kind() ipld.Kind {
	return ipld.Kind_Bool
}
func (x Bool) LookupByString(string) (ipld.Node, error) {
	return nil, ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "LookupByString", AppropriateKind: ipld.KindSet_JustMap, ActualKind: ipld.Kind_Bool}
}
func (x Bool) LookupByNode(key ipld.Node) (ipld.Node, error) {
	return nil, ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "LookupByNode", AppropriateKind: ipld.KindSet_JustMap, ActualKind: ipld.Kind_Bool}
}
func (x Bool) LookupByIndex(idx int64) (ipld.Node, error) {
	return nil, ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "LookupByIndex", AppropriateKind: ipld.KindSet_JustList, ActualKind: ipld.Kind_Bool}
}
func (x Bool) LookupBySegment(ipld.PathSegment) (ipld.Node, error) {
	return nil, ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "LookupBySegment", AppropriateKind: ipld.KindSet_Recursive, ActualKind: ipld.Kind_Bool}
}
func (Bool) MapIterator() ipld.MapIterator {
	return nil
}
func (Bool) ListIterator() ipld.ListIterator {
	return nil
}
func (Bool) Length() int64 {
	return -1
}
func (Bool) IsAbsent() bool {
	return false
}
func (Bool) IsNull() bool {
	return false
}
func (x Bool) AsInt() (int64, error) {
	return 0, ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AsInt", AppropriateKind: ipld.KindSet_JustInt, ActualKind: ipld.Kind_Bool}
}
func (x Bool) AsFloat() (float64, error) {
	return 0, ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AsFloat", AppropriateKind: ipld.KindSet_JustFloat, ActualKind: ipld.Kind_Bool}
}
func (x Bool) AsString() (string, error) {
	return "", ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AsString", AppropriateKind: ipld.KindSet_JustString, ActualKind: ipld.Kind_Bool}
}
func (x Bool) AsBytes() ([]byte, error) {
	return nil, ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AsBytes", AppropriateKind: ipld.KindSet_JustBytes, ActualKind: ipld.Kind_Bool}
}
func (x Bool) AsLink() (ipld.Link, error) {
	return nil, ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AsLink", AppropriateKind: ipld.KindSet_JustLink, ActualKind: ipld.Kind_Bool}
}

// BoolAssembler has similar purpose as Bool, but for (you guessed it)
// the NodeAssembler interface rather than the Node interface.
type BoolAssembler struct {
	TypeName string
}

func (x BoolAssembler) BeginMap(sizeHint int64) (ipld.MapAssembler, error) {
	return nil, ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "BeginMap", AppropriateKind: ipld.KindSet_JustMap, ActualKind: ipld.Kind_Bool}
}
func (x BoolAssembler) BeginList(sizeHint int64) (ipld.ListAssembler, error) {
	return nil, ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "BeginList", AppropriateKind: ipld.KindSet_JustList, ActualKind: ipld.Kind_Bool}
}
func (x BoolAssembler) AssignNull() error {
	return ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AssignNull", AppropriateKind: ipld.KindSet_JustNull, ActualKind: ipld.Kind_Bool}
}
func (x BoolAssembler) AssignInt(int64) error {
	return ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AssignInt", AppropriateKind: ipld.KindSet_JustInt, ActualKind: ipld.Kind_Bool}
}
func (x BoolAssembler) AssignFloat(float64) error {
	return ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AssignFloat", AppropriateKind: ipld.KindSet_JustFloat, ActualKind: ipld.Kind_Bool}
}
func (x BoolAssembler) AssignString(string) error {
	return ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AssignString", AppropriateKind: ipld.KindSet_JustString, ActualKind: ipld.Kind_Bool}
}
func (x BoolAssembler) AssignBytes([]byte) error {
	return ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AssignBytes", AppropriateKind: ipld.KindSet_JustBytes, ActualKind: ipld.Kind_Bool}
}
func (x BoolAssembler) AssignLink(ipld.Link) error {
	return ipld.ErrWrongKind{TypeName: x.TypeName, MethodName: "AssignLink", AppropriateKind: ipld.KindSet_JustLink, ActualKind: ipld.Kind_Bool}
}
