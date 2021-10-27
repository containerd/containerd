package basicnode

import (
	ipld "github.com/ipld/go-ipld-prime"
	"github.com/ipld/go-ipld-prime/node/mixins"
)

var (
	_ ipld.Node          = plainBytes(nil)
	_ ipld.NodePrototype = Prototype__Bytes{}
	_ ipld.NodeBuilder   = &plainBytes__Builder{}
	_ ipld.NodeAssembler = &plainBytes__Assembler{}
)

func NewBytes(value []byte) ipld.Node {
	v := plainBytes(value)
	return &v
}

// plainBytes is a simple boxed byte slice that complies with ipld.Node.
type plainBytes []byte

// -- Node interface methods -->

func (plainBytes) Kind() ipld.Kind {
	return ipld.Kind_Bytes
}
func (plainBytes) LookupByString(string) (ipld.Node, error) {
	return mixins.Bytes{TypeName: "bytes"}.LookupByString("")
}
func (plainBytes) LookupByNode(key ipld.Node) (ipld.Node, error) {
	return mixins.Bytes{TypeName: "bytes"}.LookupByNode(nil)
}
func (plainBytes) LookupByIndex(idx int64) (ipld.Node, error) {
	return mixins.Bytes{TypeName: "bytes"}.LookupByIndex(0)
}
func (plainBytes) LookupBySegment(seg ipld.PathSegment) (ipld.Node, error) {
	return mixins.Bytes{TypeName: "bytes"}.LookupBySegment(seg)
}
func (plainBytes) MapIterator() ipld.MapIterator {
	return nil
}
func (plainBytes) ListIterator() ipld.ListIterator {
	return nil
}
func (plainBytes) Length() int64 {
	return -1
}
func (plainBytes) IsAbsent() bool {
	return false
}
func (plainBytes) IsNull() bool {
	return false
}
func (plainBytes) AsBool() (bool, error) {
	return mixins.Bytes{TypeName: "bytes"}.AsBool()
}
func (plainBytes) AsInt() (int64, error) {
	return mixins.Bytes{TypeName: "bytes"}.AsInt()
}
func (plainBytes) AsFloat() (float64, error) {
	return mixins.Bytes{TypeName: "bytes"}.AsFloat()
}
func (plainBytes) AsString() (string, error) {
	return mixins.Bytes{TypeName: "bytes"}.AsString()
}
func (n plainBytes) AsBytes() ([]byte, error) {
	return []byte(n), nil
}
func (plainBytes) AsLink() (ipld.Link, error) {
	return mixins.Bytes{TypeName: "bytes"}.AsLink()
}
func (plainBytes) Prototype() ipld.NodePrototype {
	return Prototype__Bytes{}
}

// -- NodePrototype -->

type Prototype__Bytes struct{}

func (Prototype__Bytes) NewBuilder() ipld.NodeBuilder {
	var w plainBytes
	return &plainBytes__Builder{plainBytes__Assembler{w: &w}}
}

// -- NodeBuilder -->

type plainBytes__Builder struct {
	plainBytes__Assembler
}

func (nb *plainBytes__Builder) Build() ipld.Node {
	return nb.w
}
func (nb *plainBytes__Builder) Reset() {
	var w plainBytes
	*nb = plainBytes__Builder{plainBytes__Assembler{w: &w}}
}

// -- NodeAssembler -->

type plainBytes__Assembler struct {
	w *plainBytes
}

func (plainBytes__Assembler) BeginMap(sizeHint int64) (ipld.MapAssembler, error) {
	return mixins.BytesAssembler{TypeName: "bytes"}.BeginMap(0)
}
func (plainBytes__Assembler) BeginList(sizeHint int64) (ipld.ListAssembler, error) {
	return mixins.BytesAssembler{TypeName: "bytes"}.BeginList(0)
}
func (plainBytes__Assembler) AssignNull() error {
	return mixins.BytesAssembler{TypeName: "bytes"}.AssignNull()
}
func (plainBytes__Assembler) AssignBool(bool) error {
	return mixins.BytesAssembler{TypeName: "bytes"}.AssignBool(false)
}
func (plainBytes__Assembler) AssignInt(int64) error {
	return mixins.BytesAssembler{TypeName: "bytes"}.AssignInt(0)
}
func (plainBytes__Assembler) AssignFloat(float64) error {
	return mixins.BytesAssembler{TypeName: "bytes"}.AssignFloat(0)
}
func (plainBytes__Assembler) AssignString(string) error {
	return mixins.BytesAssembler{TypeName: "bytes"}.AssignString("")
}
func (na *plainBytes__Assembler) AssignBytes(v []byte) error {
	*na.w = plainBytes(v)
	return nil
}
func (plainBytes__Assembler) AssignLink(ipld.Link) error {
	return mixins.BytesAssembler{TypeName: "bytes"}.AssignLink(nil)
}
func (na *plainBytes__Assembler) AssignNode(v ipld.Node) error {
	if v2, err := v.AsBytes(); err != nil {
		return err
	} else {
		*na.w = plainBytes(v2)
		return nil
	}
}
func (plainBytes__Assembler) Prototype() ipld.NodePrototype {
	return Prototype__Bytes{}
}
