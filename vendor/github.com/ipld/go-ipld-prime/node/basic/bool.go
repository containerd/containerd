package basicnode

import (
	ipld "github.com/ipld/go-ipld-prime"
	"github.com/ipld/go-ipld-prime/node/mixins"
)

var (
	_ ipld.Node          = plainBool(false)
	_ ipld.NodePrototype = Prototype__Bool{}
	_ ipld.NodeBuilder   = &plainBool__Builder{}
	_ ipld.NodeAssembler = &plainBool__Assembler{}
)

func NewBool(value bool) ipld.Node {
	v := plainBool(value)
	return &v
}

// plainBool is a simple boxed boolean that complies with ipld.Node.
type plainBool bool

// -- Node interface methods -->

func (plainBool) Kind() ipld.Kind {
	return ipld.Kind_Bool
}
func (plainBool) LookupByString(string) (ipld.Node, error) {
	return mixins.Bool{TypeName: "bool"}.LookupByString("")
}
func (plainBool) LookupByNode(key ipld.Node) (ipld.Node, error) {
	return mixins.Bool{TypeName: "bool"}.LookupByNode(nil)
}
func (plainBool) LookupByIndex(idx int64) (ipld.Node, error) {
	return mixins.Bool{TypeName: "bool"}.LookupByIndex(0)
}
func (plainBool) LookupBySegment(seg ipld.PathSegment) (ipld.Node, error) {
	return mixins.Bool{TypeName: "bool"}.LookupBySegment(seg)
}
func (plainBool) MapIterator() ipld.MapIterator {
	return nil
}
func (plainBool) ListIterator() ipld.ListIterator {
	return nil
}
func (plainBool) Length() int64 {
	return -1
}
func (plainBool) IsAbsent() bool {
	return false
}
func (plainBool) IsNull() bool {
	return false
}
func (n plainBool) AsBool() (bool, error) {
	return bool(n), nil
}
func (plainBool) AsInt() (int64, error) {
	return mixins.Bool{TypeName: "bool"}.AsInt()
}
func (plainBool) AsFloat() (float64, error) {
	return mixins.Bool{TypeName: "bool"}.AsFloat()
}
func (plainBool) AsString() (string, error) {
	return mixins.Bool{TypeName: "bool"}.AsString()
}
func (plainBool) AsBytes() ([]byte, error) {
	return mixins.Bool{TypeName: "bool"}.AsBytes()
}
func (plainBool) AsLink() (ipld.Link, error) {
	return mixins.Bool{TypeName: "bool"}.AsLink()
}
func (plainBool) Prototype() ipld.NodePrototype {
	return Prototype__Bool{}
}

// -- NodePrototype -->

type Prototype__Bool struct{}

func (Prototype__Bool) NewBuilder() ipld.NodeBuilder {
	var w plainBool
	return &plainBool__Builder{plainBool__Assembler{w: &w}}
}

// -- NodeBuilder -->

type plainBool__Builder struct {
	plainBool__Assembler
}

func (nb *plainBool__Builder) Build() ipld.Node {
	return nb.w
}
func (nb *plainBool__Builder) Reset() {
	var w plainBool
	*nb = plainBool__Builder{plainBool__Assembler{w: &w}}
}

// -- NodeAssembler -->

type plainBool__Assembler struct {
	w *plainBool
}

func (plainBool__Assembler) BeginMap(sizeHint int64) (ipld.MapAssembler, error) {
	return mixins.BoolAssembler{TypeName: "bool"}.BeginMap(0)
}
func (plainBool__Assembler) BeginList(sizeHint int64) (ipld.ListAssembler, error) {
	return mixins.BoolAssembler{TypeName: "bool"}.BeginList(0)
}
func (plainBool__Assembler) AssignNull() error {
	return mixins.BoolAssembler{TypeName: "bool"}.AssignNull()
}
func (na *plainBool__Assembler) AssignBool(v bool) error {
	*na.w = plainBool(v)
	return nil
}
func (plainBool__Assembler) AssignInt(int64) error {
	return mixins.BoolAssembler{TypeName: "bool"}.AssignInt(0)
}
func (plainBool__Assembler) AssignFloat(float64) error {
	return mixins.BoolAssembler{TypeName: "bool"}.AssignFloat(0)
}
func (plainBool__Assembler) AssignString(string) error {
	return mixins.BoolAssembler{TypeName: "bool"}.AssignString("")
}
func (plainBool__Assembler) AssignBytes([]byte) error {
	return mixins.BoolAssembler{TypeName: "bool"}.AssignBytes(nil)
}
func (plainBool__Assembler) AssignLink(ipld.Link) error {
	return mixins.BoolAssembler{TypeName: "bool"}.AssignLink(nil)
}
func (na *plainBool__Assembler) AssignNode(v ipld.Node) error {
	if v2, err := v.AsBool(); err != nil {
		return err
	} else {
		*na.w = plainBool(v2)
		return nil
	}
}
func (plainBool__Assembler) Prototype() ipld.NodePrototype {
	return Prototype__Bool{}
}
