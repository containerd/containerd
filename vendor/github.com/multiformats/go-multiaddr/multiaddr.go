package multiaddr

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"strings"
)

// multiaddr is the data structure representing a Multiaddr
type multiaddr struct {
	bytes []byte
}

// NewMultiaddr parses and validates an input string, returning a *Multiaddr
func NewMultiaddr(s string) (a Multiaddr, err error) {
	defer func() {
		if e := recover(); e != nil {
			log.Printf("Panic in NewMultiaddr on input %q: %s", s, e)
			err = fmt.Errorf("%v", e)
		}
	}()
	b, err := stringToBytes(s)
	if err != nil {
		return nil, err
	}
	return &multiaddr{bytes: b}, nil
}

// NewMultiaddrBytes initializes a Multiaddr from a byte representation.
// It validates it as an input string.
func NewMultiaddrBytes(b []byte) (a Multiaddr, err error) {
	defer func() {
		if e := recover(); e != nil {
			log.Printf("Panic in NewMultiaddrBytes on input %q: %s", b, e)
			err = fmt.Errorf("%v", e)
		}
	}()

	if err := validateBytes(b); err != nil {
		return nil, err
	}

	return &multiaddr{bytes: b}, nil
}

// Equal tests whether two multiaddrs are equal
func (m *multiaddr) Equal(m2 Multiaddr) bool {
	return bytes.Equal(m.bytes, m2.Bytes())
}

// Bytes returns the []byte representation of this Multiaddr
//
// Do not modify the returned buffer, it may be shared.
func (m *multiaddr) Bytes() []byte {
	return m.bytes
}

// String returns the string representation of a Multiaddr
func (m *multiaddr) String() string {
	s, err := bytesToString(m.bytes)
	if err != nil {
		panic(fmt.Errorf("multiaddr failed to convert back to string. corrupted? %s", err))
	}
	return s
}

func (m *multiaddr) MarshalBinary() ([]byte, error) {
	return m.Bytes(), nil
}

func (m *multiaddr) UnmarshalBinary(data []byte) error {
	new, err := NewMultiaddrBytes(data)
	if err != nil {
		return err
	}
	*m = *(new.(*multiaddr))
	return nil
}

func (m *multiaddr) MarshalText() ([]byte, error) {
	return []byte(m.String()), nil
}

func (m *multiaddr) UnmarshalText(data []byte) error {
	new, err := NewMultiaddr(string(data))
	if err != nil {
		return err
	}
	*m = *(new.(*multiaddr))
	return nil
}

func (m *multiaddr) MarshalJSON() ([]byte, error) {
	return json.Marshal(m.String())
}

func (m *multiaddr) UnmarshalJSON(data []byte) error {
	var v string
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	new, err := NewMultiaddr(v)
	*m = *(new.(*multiaddr))
	return err
}

// Protocols returns the list of protocols this Multiaddr has.
// will panic in case we access bytes incorrectly.
func (m *multiaddr) Protocols() []Protocol {
	ps := make([]Protocol, 0, 8)
	b := m.bytes
	for len(b) > 0 {
		code, n, err := ReadVarintCode(b)
		if err != nil {
			panic(err)
		}

		p := ProtocolWithCode(code)
		if p.Code == 0 {
			// this is a panic (and not returning err) because this should've been
			// caught on constructing the Multiaddr
			panic(fmt.Errorf("no protocol with code %d", b[0]))
		}
		ps = append(ps, p)
		b = b[n:]

		n, size, err := sizeForAddr(p, b)
		if err != nil {
			panic(err)
		}

		b = b[n+size:]
	}
	return ps
}

// Encapsulate wraps a given Multiaddr, returning the resulting joined Multiaddr
func (m *multiaddr) Encapsulate(o Multiaddr) Multiaddr {
	mb := m.bytes
	ob := o.Bytes()

	b := make([]byte, len(mb)+len(ob))
	copy(b, mb)
	copy(b[len(mb):], ob)
	return &multiaddr{bytes: b}
}

// Decapsulate unwraps Multiaddr up until the given Multiaddr is found.
func (m *multiaddr) Decapsulate(o Multiaddr) Multiaddr {
	s1 := m.String()
	s2 := o.String()
	i := strings.LastIndex(s1, s2)
	if i < 0 {
		// if multiaddr not contained, returns a copy.
		cpy := make([]byte, len(m.bytes))
		copy(cpy, m.bytes)
		return &multiaddr{bytes: cpy}
	}

	if i == 0 {
		return nil
	}

	ma, err := NewMultiaddr(s1[:i])
	if err != nil {
		panic("Multiaddr.Decapsulate incorrect byte boundaries.")
	}
	return ma
}

var ErrProtocolNotFound = fmt.Errorf("protocol not found in multiaddr")

func (m *multiaddr) ValueForProtocol(code int) (value string, err error) {
	err = ErrProtocolNotFound
	ForEach(m, func(c Component) bool {
		if c.Protocol().Code == code {
			value = c.Value()
			err = nil
			return false
		}
		return true
	})
	return
}
