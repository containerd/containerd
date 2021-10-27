package basicnode

import (
	ipld "github.com/ipld/go-ipld-prime"
	"github.com/ipld/go-ipld-prime/node/mixins"
)

var (
	_ ipld.Node          = &plainLink{}
	_ ipld.NodePrototype = Prototype__Link{}
	_ ipld.NodeBuilder   = &plainLink__Builder{}
	_ ipld.NodeAssembler = &plainLink__Assembler{}
)

func NewLink(value ipld.Link) ipld.Node {
	return &plainLink{value}
}

// plainLink is a simple box around a Link that complies with ipld.Node.
type plainLink struct {
	x ipld.Link
}

// -- Node interface methods -->

func (plainLink) Kind() ipld.Kind {
	return ipld.Kind_Link
}
func (plainLink) LookupByString(string) (ipld.Node, error) {
	return mixins.Link{TypeName: "link"}.LookupByString("")
}
func (plainLink) LookupByNode(key ipld.Node) (ipld.Node, error) {
	return mixins.Link{TypeName: "link"}.LookupByNode(nil)
}
func (plainLink) LookupByIndex(idx int64) (ipld.Node, error) {
	return mixins.Link{TypeName: "link"}.LookupByIndex(0)
}
func (plainLink) LookupBySegment(seg ipld.PathSegment) (ipld.Node, error) {
	return mixins.Link{TypeName: "link"}.LookupBySegment(seg)
}
func (plainLink) MapIterator() ipld.MapIterator {
	return nil
}
func (plainLink) ListIterator() ipld.ListIterator {
	return nil
}
func (plainLink) Length() int64 {
	return -1
}
func (plainLink) IsAbsent() bool {
	return false
}
func (plainLink) IsNull() bool {
	return false
}
func (plainLink) AsBool() (bool, error) {
	return mixins.Link{TypeName: "link"}.AsBool()
}
func (plainLink) AsInt() (int64, error) {
	return mixins.Link{TypeName: "link"}.AsInt()
}
func (plainLink) AsFloat() (float64, error) {
	return mixins.Link{TypeName: "link"}.AsFloat()
}
func (plainLink) AsString() (string, error) {
	return mixins.Link{TypeName: "link"}.AsString()
}
func (plainLink) AsBytes() ([]byte, error) {
	return mixins.Link{TypeName: "link"}.AsBytes()
}
func (n *plainLink) AsLink() (ipld.Link, error) {
	return n.x, nil
}
func (plainLink) Prototype() ipld.NodePrototype {
	return Prototype__Link{}
}

// -- NodePrototype -->

type Prototype__Link struct{}

func (Prototype__Link) NewBuilder() ipld.NodeBuilder {
	var w plainLink
	return &plainLink__Builder{plainLink__Assembler{w: &w}}
}

// -- NodeBuilder -->

type plainLink__Builder struct {
	plainLink__Assembler
}

func (nb *plainLink__Builder) Build() ipld.Node {
	return nb.w
}
func (nb *plainLink__Builder) Reset() {
	var w plainLink
	*nb = plainLink__Builder{plainLink__Assembler{w: &w}}
}

// -- NodeAssembler -->

type plainLink__Assembler struct {
	w *plainLink
}

func (plainLink__Assembler) BeginMap(sizeHint int64) (ipld.MapAssembler, error) {
	return mixins.LinkAssembler{TypeName: "link"}.BeginMap(0)
}
func (plainLink__Assembler) BeginList(sizeHint int64) (ipld.ListAssembler, error) {
	return mixins.LinkAssembler{TypeName: "link"}.BeginList(0)
}
func (plainLink__Assembler) AssignNull() error {
	return mixins.LinkAssembler{TypeName: "link"}.AssignNull()
}
func (plainLink__Assembler) AssignBool(bool) error {
	return mixins.LinkAssembler{TypeName: "link"}.AssignBool(false)
}
func (plainLink__Assembler) AssignInt(int64) error {
	return mixins.LinkAssembler{TypeName: "link"}.AssignInt(0)
}
func (plainLink__Assembler) AssignFloat(float64) error {
	return mixins.LinkAssembler{TypeName: "link"}.AssignFloat(0)
}
func (plainLink__Assembler) AssignString(string) error {
	return mixins.LinkAssembler{TypeName: "link"}.AssignString("")
}
func (plainLink__Assembler) AssignBytes([]byte) error {
	return mixins.LinkAssembler{TypeName: "link"}.AssignBytes(nil)
}
func (na *plainLink__Assembler) AssignLink(v ipld.Link) error {
	na.w.x = v
	return nil
}
func (na *plainLink__Assembler) AssignNode(v ipld.Node) error {
	if v2, err := v.AsLink(); err != nil {
		return err
	} else {
		na.w.x = v2
		return nil
	}
}
func (plainLink__Assembler) Prototype() ipld.NodePrototype {
	return Prototype__Link{}
}
