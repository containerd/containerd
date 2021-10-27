package multiaddr

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/multiformats/go-varint"
)

func stringToBytes(s string) ([]byte, error) {
	// consume trailing slashes
	s = strings.TrimRight(s, "/")

	var b bytes.Buffer
	sp := strings.Split(s, "/")

	if sp[0] != "" {
		return nil, fmt.Errorf("failed to parse multiaddr %q: must begin with /", s)
	}

	// consume first empty elem
	sp = sp[1:]

	if len(sp) == 0 {
		return nil, fmt.Errorf("failed to parse multiaddr %q: empty multiaddr", s)
	}

	for len(sp) > 0 {
		name := sp[0]
		p := ProtocolWithName(name)
		if p.Code == 0 {
			return nil, fmt.Errorf("failed to parse multiaddr %q: unknown protocol %s", s, sp[0])
		}
		_, _ = b.Write(p.VCode)
		sp = sp[1:]

		if p.Size == 0 { // no length.
			continue
		}

		if len(sp) < 1 {
			return nil, fmt.Errorf("failed to parse multiaddr %q: unexpected end of multiaddr", s)
		}

		if p.Path {
			// it's a path protocol (terminal).
			// consume the rest of the address as the next component.
			sp = []string{"/" + strings.Join(sp, "/")}
		}

		a, err := p.Transcoder.StringToBytes(sp[0])
		if err != nil {
			return nil, fmt.Errorf("failed to parse multiaddr %q: invalid value %q for protocol %s: %s", s, sp[0], p.Name, err)
		}
		if p.Size < 0 { // varint size.
			_, _ = b.Write(varint.ToUvarint(uint64(len(a))))
		}
		b.Write(a)
		sp = sp[1:]
	}

	return b.Bytes(), nil
}

func validateBytes(b []byte) (err error) {
	if len(b) == 0 {
		return fmt.Errorf("empty multiaddr")
	}
	for len(b) > 0 {
		code, n, err := ReadVarintCode(b)
		if err != nil {
			return err
		}

		b = b[n:]
		p := ProtocolWithCode(code)
		if p.Code == 0 {
			return fmt.Errorf("no protocol with code %d", code)
		}

		if p.Size == 0 {
			continue
		}

		n, size, err := sizeForAddr(p, b)
		if err != nil {
			return err
		}

		b = b[n:]

		if len(b) < size || size < 0 {
			return fmt.Errorf("invalid value for size %d", len(b))
		}

		err = p.Transcoder.ValidateBytes(b[:size])
		if err != nil {
			return err
		}

		b = b[size:]
	}

	return nil
}

func readComponent(b []byte) (int, Component, error) {
	var offset int
	code, n, err := ReadVarintCode(b)
	if err != nil {
		return 0, Component{}, err
	}
	offset += n

	p := ProtocolWithCode(code)
	if p.Code == 0 {
		return 0, Component{}, fmt.Errorf("no protocol with code %d", code)
	}

	if p.Size == 0 {
		return offset, Component{
			bytes:    b[:offset],
			offset:   offset,
			protocol: p,
		}, nil
	}

	n, size, err := sizeForAddr(p, b[offset:])
	if err != nil {
		return 0, Component{}, err
	}

	offset += n

	if len(b[offset:]) < size || size < 0 {
		return 0, Component{}, fmt.Errorf("invalid value for size %d", len(b[offset:]))
	}

	return offset + size, Component{
		bytes:    b[:offset+size],
		protocol: p,
		offset:   offset,
	}, nil
}

func bytesToString(b []byte) (ret string, err error) {
	if len(b) == 0 {
		return "", fmt.Errorf("empty multiaddr")
	}
	var buf strings.Builder

	for len(b) > 0 {
		n, c, err := readComponent(b)
		if err != nil {
			return "", err
		}
		b = b[n:]
		c.writeTo(&buf)
	}

	return buf.String(), nil
}

func sizeForAddr(p Protocol, b []byte) (skip, size int, err error) {
	switch {
	case p.Size > 0:
		return 0, (p.Size / 8), nil
	case p.Size == 0:
		return 0, 0, nil
	default:
		size, n, err := ReadVarintCode(b)
		if err != nil {
			return 0, 0, err
		}
		return n, size, nil
	}
}

func bytesSplit(b []byte) ([][]byte, error) {
	var ret [][]byte
	for len(b) > 0 {
		code, n, err := ReadVarintCode(b)
		if err != nil {
			return nil, err
		}

		p := ProtocolWithCode(code)
		if p.Code == 0 {
			return nil, fmt.Errorf("no protocol with code %d", b[0])
		}

		n2, size, err := sizeForAddr(p, b[n:])
		if err != nil {
			return nil, err
		}

		length := n + n2 + size
		ret = append(ret, b[:length])
		b = b[length:]
	}

	return ret, nil
}
