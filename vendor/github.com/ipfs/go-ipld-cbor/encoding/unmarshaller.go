package encoding

import (
	"bytes"
	"io"
	"sync"

	cbor "github.com/polydawn/refmt/cbor"
	"github.com/polydawn/refmt/obj/atlas"
)

type proxyReader struct {
	r io.Reader
}

func (r *proxyReader) Read(b []byte) (int, error) {
	return r.r.Read(b)
}

// Unmarshaller is a reusable CBOR unmarshaller.
type Unmarshaller struct {
	unmarshal *cbor.Unmarshaller
	reader    proxyReader
}

// NewUnmarshallerAtlased creates a new reusable unmarshaller.
func NewUnmarshallerAtlased(atl atlas.Atlas) *Unmarshaller {
	m := new(Unmarshaller)
	m.unmarshal = cbor.NewUnmarshallerAtlased(cbor.DecodeOptions{CoerceUndefToNull: true}, &m.reader, atl)
	return m
}

type cborUnmarshaler interface {
	UnmarshalCBOR(r io.Reader) error
}

// Decode reads a CBOR object from the given reader and decodes it into the
// given object.
func (m *Unmarshaller) Decode(r io.Reader, obj interface{}) (err error) {
	m.reader.r = r
	selfUnmarshaler, ok := obj.(cborUnmarshaler)
	if ok {
		err = selfUnmarshaler.UnmarshalCBOR(r)
	} else {
		err = m.unmarshal.Unmarshal(obj)
	}
	m.reader.r = nil
	return err
}

// Unmarshal unmarshals the given CBOR byte slice into the given object.
func (m *Unmarshaller) Unmarshal(b []byte, obj interface{}) error {
	return m.Decode(bytes.NewReader(b), obj)
}

// PooledUnmarshaller is a thread-safe pooled CBOR unmarshaller.
type PooledUnmarshaller struct {
	pool sync.Pool
}

// NewPooledUnmarshaller returns a PooledUnmarshaller with the given atlas. Do
// not copy after use.
func NewPooledUnmarshaller(atl atlas.Atlas) PooledUnmarshaller {
	return PooledUnmarshaller{
		pool: sync.Pool{
			New: func() interface{} {
				return NewUnmarshallerAtlased(atl)
			},
		},
	}
}

// Decode decodes an object from the passed reader into the given object using
// the pool of unmarshallers.
func (p *PooledUnmarshaller) Decode(r io.Reader, obj interface{}) error {
	u := p.pool.Get().(*Unmarshaller)
	err := u.Decode(r, obj)
	p.pool.Put(u)
	return err
}

// Unmarshal unmarshals the passed object using the pool of unmarshallers.
func (p *PooledUnmarshaller) Unmarshal(b []byte, obj interface{}) error {
	u := p.pool.Get().(*Unmarshaller)
	err := u.Unmarshal(b, obj)
	p.pool.Put(u)
	return err
}
