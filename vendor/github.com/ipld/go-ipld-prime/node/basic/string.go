package basicnode

import (
	ipld "github.com/ipld/go-ipld-prime"
	"github.com/ipld/go-ipld-prime/node/mixins"
)

var (
	_ ipld.Node          = plainString("")
	_ ipld.NodePrototype = Prototype__String{}
	_ ipld.NodeBuilder   = &plainString__Builder{}
	_ ipld.NodeAssembler = &plainString__Assembler{}
)

func NewString(value string) ipld.Node {
	v := plainString(value)
	return &v
}

// plainString is a simple boxed string that complies with ipld.Node.
// It's useful for many things, such as boxing map keys.
//
// The implementation is a simple typedef of a string;
// handling it as a Node incurs 'runtime.convTstring',
// which is about the best we can do.
type plainString string

// -- Node interface methods -->

func (plainString) Kind() ipld.Kind {
	return ipld.Kind_String
}
func (plainString) LookupByString(string) (ipld.Node, error) {
	return mixins.String{TypeName: "string"}.LookupByString("")
}
func (plainString) LookupByNode(key ipld.Node) (ipld.Node, error) {
	return mixins.String{TypeName: "string"}.LookupByNode(nil)
}
func (plainString) LookupByIndex(idx int64) (ipld.Node, error) {
	return mixins.String{TypeName: "string"}.LookupByIndex(0)
}
func (plainString) LookupBySegment(seg ipld.PathSegment) (ipld.Node, error) {
	return mixins.String{TypeName: "string"}.LookupBySegment(seg)
}
func (plainString) MapIterator() ipld.MapIterator {
	return nil
}
func (plainString) ListIterator() ipld.ListIterator {
	return nil
}
func (plainString) Length() int64 {
	return -1
}
func (plainString) IsAbsent() bool {
	return false
}
func (plainString) IsNull() bool {
	return false
}
func (plainString) AsBool() (bool, error) {
	return mixins.String{TypeName: "string"}.AsBool()
}
func (plainString) AsInt() (int64, error) {
	return mixins.String{TypeName: "string"}.AsInt()
}
func (plainString) AsFloat() (float64, error) {
	return mixins.String{TypeName: "string"}.AsFloat()
}
func (x plainString) AsString() (string, error) {
	return string(x), nil
}
func (plainString) AsBytes() ([]byte, error) {
	return mixins.String{TypeName: "string"}.AsBytes()
}
func (plainString) AsLink() (ipld.Link, error) {
	return mixins.String{TypeName: "string"}.AsLink()
}
func (plainString) Prototype() ipld.NodePrototype {
	return Prototype__String{}
}

// -- NodePrototype -->

type Prototype__String struct{}

func (Prototype__String) NewBuilder() ipld.NodeBuilder {
	var w plainString
	return &plainString__Builder{plainString__Assembler{w: &w}}
}

// -- NodeBuilder -->

type plainString__Builder struct {
	plainString__Assembler
}

func (nb *plainString__Builder) Build() ipld.Node {
	return nb.w
}
func (nb *plainString__Builder) Reset() {
	var w plainString
	*nb = plainString__Builder{plainString__Assembler{w: &w}}
}

// -- NodeAssembler -->

type plainString__Assembler struct {
	w *plainString
}

func (plainString__Assembler) BeginMap(sizeHint int64) (ipld.MapAssembler, error) {
	return mixins.StringAssembler{TypeName: "string"}.BeginMap(0)
}
func (plainString__Assembler) BeginList(sizeHint int64) (ipld.ListAssembler, error) {
	return mixins.StringAssembler{TypeName: "string"}.BeginList(0)
}
func (plainString__Assembler) AssignNull() error {
	return mixins.StringAssembler{TypeName: "string"}.AssignNull()
}
func (plainString__Assembler) AssignBool(bool) error {
	return mixins.StringAssembler{TypeName: "string"}.AssignBool(false)
}
func (plainString__Assembler) AssignInt(int64) error {
	return mixins.StringAssembler{TypeName: "string"}.AssignInt(0)
}
func (plainString__Assembler) AssignFloat(float64) error {
	return mixins.StringAssembler{TypeName: "string"}.AssignFloat(0)
}
func (na *plainString__Assembler) AssignString(v string) error {
	*na.w = plainString(v)
	return nil
}
func (plainString__Assembler) AssignBytes([]byte) error {
	return mixins.StringAssembler{TypeName: "string"}.AssignBytes(nil)
}
func (plainString__Assembler) AssignLink(ipld.Link) error {
	return mixins.StringAssembler{TypeName: "string"}.AssignLink(nil)
}
func (na *plainString__Assembler) AssignNode(v ipld.Node) error {
	if v2, err := v.AsString(); err != nil {
		return err
	} else {
		*na.w = plainString(v2)
		return nil
	}
}
func (plainString__Assembler) Prototype() ipld.NodePrototype {
	return Prototype__String{}
}
