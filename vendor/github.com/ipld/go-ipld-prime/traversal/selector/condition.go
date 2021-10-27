package selector

import (
	"fmt"

	ipld "github.com/ipld/go-ipld-prime"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
)

// Condition provides a mechanism for matching and limiting matching and
// exploration of selectors.
// Not all types of conditions which are imagined in the selector specification
// are encoded at present, instead we currently only implement a subset that
// is sufficient for initial pressing use cases.
type Condition struct {
	mode  ConditionMode
	match ipld.Node
}

// A ConditionMode is the keyed representation for the union that is the condition
type ConditionMode string

const (
	ConditionMode_Link ConditionMode = "/"
)

// Match decides if a given ipld.Node matches the condition.
func (c *Condition) Match(n ipld.Node) bool {
	switch c.mode {
	case ConditionMode_Link:
		if n.Kind() != ipld.Kind_Link {
			return false
		}
		lnk, err := n.AsLink()
		if err != nil {
			return false
		}
		match, err := c.match.AsLink()
		if err != nil {
			return false
		}
		cidlnk, ok := lnk.(cidlink.Link)
		cidmatch, ok2 := match.(cidlink.Link)
		if ok && ok2 {
			return cidmatch.Equals(cidlnk.Cid)
		}
		return match.String() == lnk.String()
	default:
		return false
	}
}

// ParseCondition assembles a Condition from a condition selector node
func (pc ParseContext) ParseCondition(n ipld.Node) (Condition, error) {
	if n.Kind() != ipld.Kind_Map {
		return Condition{}, fmt.Errorf("selector spec parse rejected: condition body must be a map")
	}
	if n.Length() != 1 {
		return Condition{}, fmt.Errorf("selector spec parse rejected: condition is a keyed union and thus must be single-entry map")
	}
	kn, v, _ := n.MapIterator().Next()
	kstr, _ := kn.AsString()
	// Switch over the single key to determine which condition body comes next.
	//  (This switch is where the keyed union discriminators concretely happen.)
	switch ConditionMode(kstr) {
	case ConditionMode_Link:
		if _, err := v.AsLink(); err != nil {
			return Condition{}, fmt.Errorf("selector spec parse rejected: condition_link must be a link")
		}
		return Condition{mode: ConditionMode_Link, match: v}, nil
	default:
		return Condition{}, fmt.Errorf("selector spec parse rejected: %q is not a known member of the condition union", kstr)
	}
}
