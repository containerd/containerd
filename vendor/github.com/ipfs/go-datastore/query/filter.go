package query

import (
	"bytes"
	"fmt"
	"strings"
)

// Filter is an object that tests ResultEntries
type Filter interface {
	// Filter returns whether an entry passes the filter
	Filter(e Entry) bool
}

// Op is a comparison operator
type Op string

var (
	Equal              = Op("==")
	NotEqual           = Op("!=")
	GreaterThan        = Op(">")
	GreaterThanOrEqual = Op(">=")
	LessThan           = Op("<")
	LessThanOrEqual    = Op("<=")
)

// FilterValueCompare is used to signal to datastores they
// should apply internal comparisons. unfortunately, there
// is no way to apply comparisons* to interface{} types in
// Go, so if the datastore doesnt have a special way to
// handle these comparisons, you must provided the
// TypedFilter to actually do filtering.
//
// [*] other than == and !=, which use reflect.DeepEqual.
type FilterValueCompare struct {
	Op    Op
	Value []byte
}

func (f FilterValueCompare) Filter(e Entry) bool {
	cmp := bytes.Compare(e.Value, f.Value)
	switch f.Op {
	case Equal:
		return cmp == 0
	case NotEqual:
		return cmp != 0
	case LessThan:
		return cmp < 0
	case LessThanOrEqual:
		return cmp <= 0
	case GreaterThan:
		return cmp > 0
	case GreaterThanOrEqual:
		return cmp >= 0
	default:
		panic(fmt.Errorf("unknown operation: %s", f.Op))
	}
}

func (f FilterValueCompare) String() string {
	return fmt.Sprintf("VALUE %s %q", f.Op, string(f.Value))
}

type FilterKeyCompare struct {
	Op  Op
	Key string
}

func (f FilterKeyCompare) Filter(e Entry) bool {
	switch f.Op {
	case Equal:
		return e.Key == f.Key
	case NotEqual:
		return e.Key != f.Key
	case GreaterThan:
		return e.Key > f.Key
	case GreaterThanOrEqual:
		return e.Key >= f.Key
	case LessThan:
		return e.Key < f.Key
	case LessThanOrEqual:
		return e.Key <= f.Key
	default:
		panic(fmt.Errorf("unknown op '%s'", f.Op))
	}
}

func (f FilterKeyCompare) String() string {
	return fmt.Sprintf("KEY %s %q", f.Op, f.Key)
}

type FilterKeyPrefix struct {
	Prefix string
}

func (f FilterKeyPrefix) Filter(e Entry) bool {
	return strings.HasPrefix(e.Key, f.Prefix)
}

func (f FilterKeyPrefix) String() string {
	return fmt.Sprintf("PREFIX(%q)", f.Prefix)
}
