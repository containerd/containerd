package multihash

// Set is a set of Multihashes, holding one copy per Multihash.
type Set struct {
	set map[string]struct{}
}

// NewSet creates a new set correctly initialized.
func NewSet() *Set {
	return &Set{
		set: make(map[string]struct{}),
	}
}

// Add adds a new multihash to the set.
func (s *Set) Add(m Multihash) {
	s.set[string(m)] = struct{}{}
}

// Len returns the number of elements in the set.
func (s *Set) Len() int {
	return len(s.set)
}

// Has returns true if the element is in the set.
func (s *Set) Has(m Multihash) bool {
	_, ok := s.set[string(m)]
	return ok
}

// Visit adds a multihash only if it is not in the set already.  Returns true
// if the multihash was added (was not in the set before).
func (s *Set) Visit(m Multihash) bool {
	_, ok := s.set[string(m)]
	if !ok {
		s.set[string(m)] = struct{}{}
		return true
	}
	return false
}

// ForEach runs f(m) with each multihash in the set. If returns immediately if
// f(m) returns an error.
func (s *Set) ForEach(f func(m Multihash) error) error {
	for elem := range s.set {
		mh := Multihash(elem)
		if err := f(mh); err != nil {
			return err
		}
	}
	return nil
}

// Remove removes an element from the set.
func (s *Set) Remove(m Multihash) {
	delete(s.set, string(m))
}

// All returns a slice with all the elements in the set.
func (s *Set) All() []Multihash {
	out := make([]Multihash, 0, len(s.set))
	for m := range s.set {
		out = append(out, Multihash(m))
	}
	return out
}
