package basicnode

import (
	ipld "github.com/ipld/go-ipld-prime"
	"github.com/ipld/go-ipld-prime/node/mixins"
)

var (
	_ ipld.Node          = &plainList{}
	_ ipld.NodePrototype = Prototype__List{}
	_ ipld.NodeBuilder   = &plainList__Builder{}
	_ ipld.NodeAssembler = &plainList__Assembler{}
)

// plainList is a concrete type that provides a list-kind ipld.Node.
// It can contain any kind of value.
// plainList is also embedded in the 'any' struct and usable from there.
type plainList struct {
	x []ipld.Node
}

// -- Node interface methods -->

func (plainList) Kind() ipld.Kind {
	return ipld.Kind_List
}
func (plainList) LookupByString(string) (ipld.Node, error) {
	return mixins.List{TypeName: "list"}.LookupByString("")
}
func (plainList) LookupByNode(ipld.Node) (ipld.Node, error) {
	return mixins.List{TypeName: "list"}.LookupByNode(nil)
}
func (n *plainList) LookupByIndex(idx int64) (ipld.Node, error) {
	if n.Length() <= idx {
		return nil, ipld.ErrNotExists{Segment: ipld.PathSegmentOfInt(idx)}
	}
	return n.x[idx], nil
}
func (n *plainList) LookupBySegment(seg ipld.PathSegment) (ipld.Node, error) {
	idx, err := seg.Index()
	if err != nil {
		return nil, ipld.ErrInvalidSegmentForList{TroubleSegment: seg, Reason: err}
	}
	return n.LookupByIndex(idx)
}
func (plainList) MapIterator() ipld.MapIterator {
	return nil
}
func (n *plainList) ListIterator() ipld.ListIterator {
	return &plainList_ListIterator{n, 0}
}
func (n *plainList) Length() int64 {
	return int64(len(n.x))
}
func (plainList) IsAbsent() bool {
	return false
}
func (plainList) IsNull() bool {
	return false
}
func (plainList) AsBool() (bool, error) {
	return mixins.List{TypeName: "list"}.AsBool()
}
func (plainList) AsInt() (int64, error) {
	return mixins.List{TypeName: "list"}.AsInt()
}
func (plainList) AsFloat() (float64, error) {
	return mixins.List{TypeName: "list"}.AsFloat()
}
func (plainList) AsString() (string, error) {
	return mixins.List{TypeName: "list"}.AsString()
}
func (plainList) AsBytes() ([]byte, error) {
	return mixins.List{TypeName: "list"}.AsBytes()
}
func (plainList) AsLink() (ipld.Link, error) {
	return mixins.List{TypeName: "list"}.AsLink()
}
func (plainList) Prototype() ipld.NodePrototype {
	return Prototype__List{}
}

type plainList_ListIterator struct {
	n   *plainList
	idx int
}

func (itr *plainList_ListIterator) Next() (idx int64, v ipld.Node, _ error) {
	if itr.Done() {
		return -1, nil, ipld.ErrIteratorOverread{}
	}
	v = itr.n.x[itr.idx]
	idx = int64(itr.idx)
	itr.idx++
	return
}
func (itr *plainList_ListIterator) Done() bool {
	return itr.idx >= len(itr.n.x)
}

// -- NodePrototype -->

type Prototype__List struct{}

func (Prototype__List) NewBuilder() ipld.NodeBuilder {
	return &plainList__Builder{plainList__Assembler{w: &plainList{}}}
}

// -- NodeBuilder -->

type plainList__Builder struct {
	plainList__Assembler
}

func (nb *plainList__Builder) Build() ipld.Node {
	if nb.state != laState_finished {
		panic("invalid state: assembler must be 'finished' before Build can be called!")
	}
	return nb.w
}
func (nb *plainList__Builder) Reset() {
	*nb = plainList__Builder{}
	nb.w = &plainList{}
}

// -- NodeAssembler -->

type plainList__Assembler struct {
	w *plainList

	va plainList__ValueAssembler

	state laState
}
type plainList__ValueAssembler struct {
	la *plainList__Assembler
}

// laState is an enum of the state machine for a list assembler.
// (this might be something to export reusably, but it's also very much an impl detail that need not be seen, so, dubious.)
// it's similar to maState for maps, but has fewer states because we never have keys to assemble.
type laState uint8

const (
	laState_initial  laState = iota // also the 'expect value or finish' state
	laState_midValue                // waiting for a 'finished' state in the ValueAssembler.
	laState_finished                // 'w' will also be nil, but this is a politer statement
)

func (plainList__Assembler) BeginMap(sizeHint int64) (ipld.MapAssembler, error) {
	return mixins.ListAssembler{TypeName: "list"}.BeginMap(0)
}
func (na *plainList__Assembler) BeginList(sizeHint int64) (ipld.ListAssembler, error) {
	if sizeHint < 0 {
		sizeHint = 0
	}
	// Allocate storage space.
	na.w.x = make([]ipld.Node, 0, sizeHint)
	// That's it; return self as the ListAssembler.  We already have all the right methods on this structure.
	return na, nil
}
func (plainList__Assembler) AssignNull() error {
	return mixins.ListAssembler{TypeName: "list"}.AssignNull()
}
func (plainList__Assembler) AssignBool(bool) error {
	return mixins.ListAssembler{TypeName: "list"}.AssignBool(false)
}
func (plainList__Assembler) AssignInt(int64) error {
	return mixins.ListAssembler{TypeName: "list"}.AssignInt(0)
}
func (plainList__Assembler) AssignFloat(float64) error {
	return mixins.ListAssembler{TypeName: "list"}.AssignFloat(0)
}
func (plainList__Assembler) AssignString(string) error {
	return mixins.ListAssembler{TypeName: "list"}.AssignString("")
}
func (plainList__Assembler) AssignBytes([]byte) error {
	return mixins.ListAssembler{TypeName: "list"}.AssignBytes(nil)
}
func (plainList__Assembler) AssignLink(ipld.Link) error {
	return mixins.ListAssembler{TypeName: "list"}.AssignLink(nil)
}
func (na *plainList__Assembler) AssignNode(v ipld.Node) error {
	// Sanity check, then update, assembler state.
	//  Update of state to 'finished' comes later; where exactly depends on if shortcuts apply.
	if na.state != laState_initial {
		panic("misuse")
	}
	// Copy the content.
	if v2, ok := v.(*plainList); ok { // if our own type: shortcut.
		// Copy the structure by value.
		//  This means we'll have pointers into the same internal maps and slices;
		//   this is okay, because the Node type promises it's immutable, and we are going to instantly finish ourselves to also maintain that.
		// FIXME: the shortcut behaves differently than the long way: it discards any existing progress.  Doesn't violate immut, but is odd.
		*na.w = *v2
		na.state = laState_finished
		return nil
	}
	// If the above shortcut didn't work, resort to a generic copy.
	//  We call AssignNode for all the child values, giving them a chance to hit shortcuts even if we didn't.
	if v.Kind() != ipld.Kind_List {
		return ipld.ErrWrongKind{TypeName: "list", MethodName: "AssignNode", AppropriateKind: ipld.KindSet_JustList, ActualKind: v.Kind()}
	}
	itr := v.ListIterator()
	for !itr.Done() {
		_, v, err := itr.Next()
		if err != nil {
			return err
		}
		if err := na.AssembleValue().AssignNode(v); err != nil {
			return err
		}
	}
	return na.Finish()
}
func (plainList__Assembler) Prototype() ipld.NodePrototype {
	return Prototype__List{}
}

// -- ListAssembler -->

// AssembleValue is part of conforming to ListAssembler, which we do on
// plainList__Assembler so that BeginList can just return a retyped pointer rather than new object.
func (la *plainList__Assembler) AssembleValue() ipld.NodeAssembler {
	// Sanity check, then update, assembler state.
	if la.state != laState_initial {
		panic("misuse")
	}
	la.state = laState_midValue
	// Make value assembler valid by giving it pointer back to whole 'la'; yield it.
	la.va.la = la
	return &la.va
}

// Finish is part of conforming to ListAssembler, which we do on
// plainList__Assembler so that BeginList can just return a retyped pointer rather than new object.
func (la *plainList__Assembler) Finish() error {
	// Sanity check, then update, assembler state.
	if la.state != laState_initial {
		panic("misuse")
	}
	la.state = laState_finished
	// validators could run and report errors promptly, if this type had any.
	return nil
}
func (plainList__Assembler) ValuePrototype(_ int64) ipld.NodePrototype {
	return Prototype__Any{}
}

// -- ListAssembler.ValueAssembler -->

func (lva *plainList__ValueAssembler) BeginMap(sizeHint int64) (ipld.MapAssembler, error) {
	ma := plainList__ValueAssemblerMap{}
	ma.ca.w = &plainMap{}
	ma.p = lva.la
	_, err := ma.ca.BeginMap(sizeHint)
	return &ma, err
}
func (lva *plainList__ValueAssembler) BeginList(sizeHint int64) (ipld.ListAssembler, error) {
	la := plainList__ValueAssemblerList{}
	la.ca.w = &plainList{}
	la.p = lva.la
	_, err := la.ca.BeginList(sizeHint)
	return &la, err
}
func (lva *plainList__ValueAssembler) AssignNull() error {
	return lva.AssignNode(ipld.Null)
}
func (lva *plainList__ValueAssembler) AssignBool(v bool) error {
	vb := plainBool(v)
	return lva.AssignNode(&vb)
}
func (lva *plainList__ValueAssembler) AssignInt(v int64) error {
	vb := plainInt(v)
	return lva.AssignNode(&vb)
}
func (lva *plainList__ValueAssembler) AssignFloat(v float64) error {
	vb := plainFloat(v)
	return lva.AssignNode(&vb)
}
func (lva *plainList__ValueAssembler) AssignString(v string) error {
	vb := plainString(v)
	return lva.AssignNode(&vb)
}
func (lva *plainList__ValueAssembler) AssignBytes(v []byte) error {
	vb := plainBytes(v)
	return lva.AssignNode(&vb)
}
func (lva *plainList__ValueAssembler) AssignLink(v ipld.Link) error {
	vb := plainLink{v}
	return lva.AssignNode(&vb)
}
func (lva *plainList__ValueAssembler) AssignNode(v ipld.Node) error {
	lva.la.w.x = append(lva.la.w.x, v)
	lva.la.state = laState_initial
	lva.la = nil // invalidate self to prevent further incorrect use.
	return nil
}
func (plainList__ValueAssembler) Prototype() ipld.NodePrototype {
	return Prototype__Any{}
}

type plainList__ValueAssemblerMap struct {
	ca plainMap__Assembler
	p  *plainList__Assembler // pointer back to parent, for final insert and state bump
}

// we briefly state only the methods we need to delegate here.
// just embedding plainMap__Assembler also behaves correctly,
//  but causes a lot of unnecessary autogenerated functions in the final binary.

func (ma *plainList__ValueAssemblerMap) AssembleEntry(k string) (ipld.NodeAssembler, error) {
	return ma.ca.AssembleEntry(k)
}
func (ma *plainList__ValueAssemblerMap) AssembleKey() ipld.NodeAssembler {
	return ma.ca.AssembleKey()
}
func (ma *plainList__ValueAssemblerMap) AssembleValue() ipld.NodeAssembler {
	return ma.ca.AssembleValue()
}
func (plainList__ValueAssemblerMap) KeyPrototype() ipld.NodePrototype {
	return Prototype__String{}
}
func (plainList__ValueAssemblerMap) ValuePrototype(_ string) ipld.NodePrototype {
	return Prototype__Any{}
}

func (ma *plainList__ValueAssemblerMap) Finish() error {
	if err := ma.ca.Finish(); err != nil {
		return err
	}
	w := ma.ca.w
	ma.ca.w = nil
	return ma.p.va.AssignNode(w)
}

type plainList__ValueAssemblerList struct {
	ca plainList__Assembler
	p  *plainList__Assembler // pointer back to parent, for final insert and state bump
}

// we briefly state only the methods we need to delegate here.
// just embedding plainList__Assembler also behaves correctly,
//  but causes a lot of unnecessary autogenerated functions in the final binary.

func (la *plainList__ValueAssemblerList) AssembleValue() ipld.NodeAssembler {
	return la.ca.AssembleValue()
}
func (plainList__ValueAssemblerList) ValuePrototype(_ int64) ipld.NodePrototype {
	return Prototype__Any{}
}

func (la *plainList__ValueAssemblerList) Finish() error {
	if err := la.ca.Finish(); err != nil {
		return err
	}
	w := la.ca.w
	la.ca.w = nil
	return la.p.va.AssignNode(w)
}
