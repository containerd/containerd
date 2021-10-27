package peer

import (
	"sync"
)

// PeerSet is a threadsafe set of peers.
type Set struct {
	lk sync.RWMutex
	ps map[ID]struct{}

	size int
}

func NewSet() *Set {
	ps := new(Set)
	ps.ps = make(map[ID]struct{})
	ps.size = -1
	return ps
}

func NewLimitedSet(size int) *Set {
	ps := new(Set)
	ps.ps = make(map[ID]struct{})
	ps.size = size
	return ps
}

func (ps *Set) Add(p ID) {
	ps.lk.Lock()
	ps.ps[p] = struct{}{}
	ps.lk.Unlock()
}

func (ps *Set) Contains(p ID) bool {
	ps.lk.RLock()
	_, ok := ps.ps[p]
	ps.lk.RUnlock()
	return ok
}

func (ps *Set) Size() int {
	ps.lk.RLock()
	defer ps.lk.RUnlock()
	return len(ps.ps)
}

// TryAdd Attempts to add the given peer into the set.
// This operation can fail for one of two reasons:
// 1) The given peer is already in the set
// 2) The number of peers in the set is equal to size
func (ps *Set) TryAdd(p ID) bool {
	var success bool
	ps.lk.Lock()
	if _, ok := ps.ps[p]; !ok && (len(ps.ps) < ps.size || ps.size == -1) {
		success = true
		ps.ps[p] = struct{}{}
	}
	ps.lk.Unlock()
	return success
}

func (ps *Set) Peers() []ID {
	ps.lk.Lock()
	out := make([]ID, 0, len(ps.ps))
	for p, _ := range ps.ps {
		out = append(out, p)
	}
	ps.lk.Unlock()
	return out
}
