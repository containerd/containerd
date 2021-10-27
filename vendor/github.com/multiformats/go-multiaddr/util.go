package multiaddr

import "fmt"

// Split returns the sub-address portions of a multiaddr.
func Split(m Multiaddr) []Multiaddr {
	if _, ok := m.(*Component); ok {
		return []Multiaddr{m}
	}
	var addrs []Multiaddr
	ForEach(m, func(c Component) bool {
		addrs = append(addrs, &c)
		return true
	})
	return addrs
}

// Join returns a combination of addresses.
func Join(ms ...Multiaddr) Multiaddr {
	switch len(ms) {
	case 0:
		// empty multiaddr, unfortunately, we have callers that rely on
		// this contract.
		return &multiaddr{}
	case 1:
		return ms[0]
	}

	length := 0
	bs := make([][]byte, len(ms))
	for i, m := range ms {
		bs[i] = m.Bytes()
		length += len(bs[i])
	}

	bidx := 0
	b := make([]byte, length)
	for _, mb := range bs {
		bidx += copy(b[bidx:], mb)
	}
	return &multiaddr{bytes: b}
}

// Cast re-casts a byte slice as a multiaddr. will panic if it fails to parse.
func Cast(b []byte) Multiaddr {
	m, err := NewMultiaddrBytes(b)
	if err != nil {
		panic(fmt.Errorf("multiaddr failed to parse: %s", err))
	}
	return m
}

// StringCast like Cast, but parses a string. Will also panic if it fails to parse.
func StringCast(s string) Multiaddr {
	m, err := NewMultiaddr(s)
	if err != nil {
		panic(fmt.Errorf("multiaddr failed to parse: %s", err))
	}
	return m
}

// SplitFirst returns the first component and the rest of the multiaddr.
func SplitFirst(m Multiaddr) (*Component, Multiaddr) {
	// Shortcut if we already have a component
	if c, ok := m.(*Component); ok {
		return c, nil
	}

	b := m.Bytes()
	if len(b) == 0 {
		return nil, nil
	}
	n, c, err := readComponent(b)
	if err != nil {
		panic(err)
	}
	if len(b) == n {
		return &c, nil
	}
	return &c, &multiaddr{b[n:]}
}

// SplitLast returns the rest of the multiaddr and the last component.
func SplitLast(m Multiaddr) (Multiaddr, *Component) {
	// Shortcut if we already have a component
	if c, ok := m.(*Component); ok {
		return nil, c
	}

	b := m.Bytes()
	if len(b) == 0 {
		return nil, nil
	}

	var (
		c      Component
		err    error
		offset int
	)
	for {
		var n int
		n, c, err = readComponent(b[offset:])
		if err != nil {
			panic(err)
		}
		if len(b) == n+offset {
			// Reached end
			if offset == 0 {
				// Only one component
				return nil, &c
			}
			return &multiaddr{b[:offset]}, &c
		}
		offset += n
	}
}

// SplitFunc splits the multiaddr when the callback first returns true. The
// component on which the callback first returns will be included in the
// *second* multiaddr.
func SplitFunc(m Multiaddr, cb func(Component) bool) (Multiaddr, Multiaddr) {
	// Shortcut if we already have a component
	if c, ok := m.(*Component); ok {
		if cb(*c) {
			return nil, m
		}
		return m, nil
	}
	b := m.Bytes()
	if len(b) == 0 {
		return nil, nil
	}
	var (
		c      Component
		err    error
		offset int
	)
	for offset < len(b) {
		var n int
		n, c, err = readComponent(b[offset:])
		if err != nil {
			panic(err)
		}
		if cb(c) {
			break
		}
		offset += n
	}
	switch offset {
	case 0:
		return nil, m
	case len(b):
		return m, nil
	default:
		return &multiaddr{b[:offset]}, &multiaddr{b[offset:]}
	}
}

// ForEach walks over the multiaddr, component by component.
//
// This function iterates over components *by value* to avoid allocating.
func ForEach(m Multiaddr, cb func(c Component) bool) {
	// Shortcut if we already have a component
	if c, ok := m.(*Component); ok {
		cb(*c)
		return
	}

	b := m.Bytes()
	for len(b) > 0 {
		n, c, err := readComponent(b)
		if err != nil {
			panic(err)
		}
		if !cb(c) {
			return
		}
		b = b[n:]
	}
}
