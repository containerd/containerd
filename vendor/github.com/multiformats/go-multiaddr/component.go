package multiaddr

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/multiformats/go-varint"
)

// Component is a single multiaddr Component.
type Component struct {
	bytes    []byte
	protocol Protocol
	offset   int
}

func (c *Component) Bytes() []byte {
	return c.bytes
}

func (c *Component) MarshalBinary() ([]byte, error) {
	return c.Bytes(), nil
}

func (c *Component) UnmarshalBinary(data []byte) error {
	_, comp, err := readComponent(data)
	if err != nil {
		return err
	}
	*c = comp
	return nil
}

func (c *Component) MarshalText() ([]byte, error) {
	return []byte(c.String()), nil
}

func (c *Component) UnmarshalText(data []byte) error {
	bytes, err := stringToBytes(string(data))
	if err != nil {
		return err
	}
	_, comp, err := readComponent(bytes)
	if err != nil {
		return err
	}
	*c = comp
	return nil
}

func (c *Component) MarshalJSON() ([]byte, error) {
	txt, err := c.MarshalText()
	if err != nil {
		return nil, err
	}

	return json.Marshal(string(txt))
}

func (m *Component) UnmarshalJSON(data []byte) error {
	var v string
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}

	return m.UnmarshalText([]byte(v))
}

func (c *Component) Equal(o Multiaddr) bool {
	return bytes.Equal(c.bytes, o.Bytes())
}

func (c *Component) Protocols() []Protocol {
	return []Protocol{c.protocol}
}

func (c *Component) Decapsulate(o Multiaddr) Multiaddr {
	if c.Equal(o) {
		return nil
	}
	return c
}

func (c *Component) Encapsulate(o Multiaddr) Multiaddr {
	m := &multiaddr{bytes: c.bytes}
	return m.Encapsulate(o)
}

func (c *Component) ValueForProtocol(code int) (string, error) {
	if c.protocol.Code != code {
		return "", ErrProtocolNotFound
	}
	return c.Value(), nil
}

func (c *Component) Protocol() Protocol {
	return c.protocol
}

func (c *Component) RawValue() []byte {
	return c.bytes[c.offset:]
}

func (c *Component) Value() string {
	if c.protocol.Transcoder == nil {
		return ""
	}
	value, err := c.protocol.Transcoder.BytesToString(c.bytes[c.offset:])
	if err != nil {
		// This Component must have been checked.
		panic(err)
	}
	return value
}

func (c *Component) String() string {
	var b strings.Builder
	c.writeTo(&b)
	return b.String()
}

// writeTo is an efficient, private function for string-formatting a multiaddr.
// Trust me, we tend to allocate a lot when doing this.
func (c *Component) writeTo(b *strings.Builder) {
	b.WriteByte('/')
	b.WriteString(c.protocol.Name)
	value := c.Value()
	if len(value) == 0 {
		return
	}
	if !(c.protocol.Path && value[0] == '/') {
		b.WriteByte('/')
	}
	b.WriteString(value)
}

// NewComponent constructs a new multiaddr component
func NewComponent(protocol, value string) (*Component, error) {
	p := ProtocolWithName(protocol)
	if p.Code == 0 {
		return nil, fmt.Errorf("unsupported protocol: %s", protocol)
	}
	if p.Transcoder != nil {
		bts, err := p.Transcoder.StringToBytes(value)
		if err != nil {
			return nil, err
		}
		return newComponent(p, bts), nil
	} else if value != "" {
		return nil, fmt.Errorf("protocol %s doesn't take a value", p.Name)
	}
	return newComponent(p, nil), nil
	// TODO: handle path /?
}

func newComponent(protocol Protocol, bvalue []byte) *Component {
	size := len(bvalue)
	size += len(protocol.VCode)
	if protocol.Size < 0 {
		size += varint.UvarintSize(uint64(len(bvalue)))
	}
	maddr := make([]byte, size)
	var offset int
	offset += copy(maddr[offset:], protocol.VCode)
	if protocol.Size < 0 {
		offset += binary.PutUvarint(maddr[offset:], uint64(len(bvalue)))
	}
	copy(maddr[offset:], bvalue)

	// For debugging
	if len(maddr) != offset+len(bvalue) {
		panic("incorrect length")
	}

	return &Component{
		bytes:    maddr,
		protocol: protocol,
		offset:   offset,
	}
}
