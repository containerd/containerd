package multiaddr

import (
	"net"
	"sync"
)

// Action is an enum modelling all possible filter actions.
type Action int32

const (
	ActionNone Action = iota // zero value.
	ActionAccept
	ActionDeny
)

type filterEntry struct {
	f      net.IPNet
	action Action
}

// Filters is a structure representing a collection of accept/deny
// net.IPNet filters, together with the DefaultAction flag, which
// represents the default filter policy.
//
// Note that the last policy added to the Filters is authoritative.
type Filters struct {
	DefaultAction Action

	mu      sync.RWMutex
	filters []*filterEntry
}

// NewFilters constructs and returns a new set of net.IPNet filters.
// By default, the new filter accepts all addresses.
func NewFilters() *Filters {
	return &Filters{
		DefaultAction: ActionAccept,
		filters:       make([]*filterEntry, 0),
	}
}

func (fs *Filters) find(ipnet net.IPNet) (int, *filterEntry) {
	s := ipnet.String()
	for idx, ft := range fs.filters {
		if ft.f.String() == s {
			return idx, ft
		}
	}
	return -1, nil
}

// AddDialFilter adds a deny rule to this Filters set. Hosts
// matching the given net.IPNet filter will be denied, unless
// another rule is added which states that they should be accepted.
//
// No effort is made to prevent duplication of filters, or to simplify
// the filters list.
//
// Deprecated: Use AddFilter().
func (fs *Filters) AddDialFilter(f *net.IPNet) {
	fs.AddFilter(*f, ActionDeny)
}

// AddFilter adds a rule to the Filters set, enforcing the desired action for
// the provided IPNet mask.
func (fs *Filters) AddFilter(ipnet net.IPNet, action Action) {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	if _, f := fs.find(ipnet); f != nil {
		f.action = action
	} else {
		fs.filters = append(fs.filters, &filterEntry{ipnet, action})
	}
}

// RemoveLiteral removes the first filter associated with the supplied IPNet,
// returning whether something was removed or not. It makes no distinction
// between whether the rule is an accept or a deny.
//
// Deprecated: use RemoveLiteral() instead.
func (fs *Filters) Remove(ipnet *net.IPNet) (removed bool) {
	return fs.RemoveLiteral(*ipnet)
}

// RemoveLiteral removes the first filter associated with the supplied IPNet,
// returning whether something was removed or not. It makes no distinction
// between whether the rule is an accept or a deny.
func (fs *Filters) RemoveLiteral(ipnet net.IPNet) (removed bool) {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	if idx, _ := fs.find(ipnet); idx != -1 {
		fs.filters = append(fs.filters[:idx], fs.filters[idx+1:]...)
		return true
	}
	return false
}

// AddrBlocked parses a ma.Multiaddr and, if a valid netip is found, it applies the
// Filter set rules, returning true if the given address should be denied, and false if
// the given address is accepted.
//
// If a parsing error occurs, or no filter matches, the Filters'
// default is returned.
//
// TODO: currently, the last filter to match wins always, but it shouldn't be that way.
//  Instead, the highest-specific last filter should win; that way more specific filters
//  override more general ones.
func (fs *Filters) AddrBlocked(a Multiaddr) (deny bool) {
	var (
		netip net.IP
		found bool
	)

	ForEach(a, func(c Component) bool {
		switch c.Protocol().Code {
		case P_IP6ZONE:
			return true
		case P_IP6, P_IP4:
			found = true
			netip = net.IP(c.RawValue())
			return false
		default:
			return false
		}
	})

	if !found {
		return fs.DefaultAction == ActionDeny
	}

	fs.mu.RLock()
	defer fs.mu.RUnlock()

	action := fs.DefaultAction
	for _, ft := range fs.filters {
		if ft.f.Contains(netip) {
			action = ft.action
		}
	}

	return action == ActionDeny
}

// Filters returns the list of DENY net.IPNet masks. For backwards compatibility.
//
// A copy of the filters is made prior to returning, so the inner state is not exposed.
//
// Deprecated: Use FiltersForAction().
func (fs *Filters) Filters() (result []*net.IPNet) {
	ffa := fs.FiltersForAction(ActionDeny)
	for _, res := range ffa {
		res := res // allocate a new copy
		result = append(result, &res)
	}
	return result
}

func (fs *Filters) ActionForFilter(ipnet net.IPNet) (action Action, ok bool) {
	if _, f := fs.find(ipnet); f != nil {
		return f.action, true
	}
	return ActionNone, false
}

// FiltersForAction returns the filters associated with the indicated action.
func (fs *Filters) FiltersForAction(action Action) (result []net.IPNet) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	for _, ff := range fs.filters {
		if ff.action == action {
			result = append(result, ff.f)
		}
	}
	return result
}
