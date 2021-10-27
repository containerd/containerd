package encoding

import (
	"bytes"
	"io"
	"sync"

	cbor "github.com/polydawn/refmt/cbor"
	"github.com/polydawn/refmt/obj/atlas"
)

type proxyWriter struct {
	w io.Writer
}

func (w *proxyWriter) Write(b []byte) (int, error) {
	return w.w.Write(b)
}

// Marshaller is a reusbale CBOR marshaller.
type Marshaller struct {
	marshal *cbor.Marshaller
	writer  proxyWriter
}

// NewMarshallerAtlased constructs a new cbor Marshaller using the given atlas.
func NewMarshallerAtlased(atl atlas.Atlas) *Marshaller {
	m := new(Marshaller)
	m.marshal = cbor.NewMarshallerAtlased(&m.writer, atl)
	return m
}

type cborMarshaler interface {
	MarshalCBOR(w io.Writer) error
}

// Encode encodes the given object to the given writer.
func (m *Marshaller) Encode(obj interface{}, w io.Writer) error {
	m.writer.w = w
	var err error
	selfMarshaling, ok := obj.(cborMarshaler)
	if ok {
		err = selfMarshaling.MarshalCBOR(w)
	} else {
		err = m.marshal.Marshal(obj)
	}
	m.writer.w = nil
	return err
}

// Marshal marshels the given object to a byte slice.
func (m *Marshaller) Marshal(obj interface{}) ([]byte, error) {
	var buf bytes.Buffer
	if err := m.Encode(obj, &buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// PooledMarshaller is a thread-safe pooled CBOR marshaller.
type PooledMarshaller struct {
	pool sync.Pool
}

// NewPooledMarshaller returns a PooledMarshaller with the given atlas. Do not
// copy after use.
func NewPooledMarshaller(atl atlas.Atlas) PooledMarshaller {
	return PooledMarshaller{
		pool: sync.Pool{
			New: func() interface{} {
				return NewMarshallerAtlased(atl)
			},
		},
	}
}

// Marshal marshals the passed object using the pool of marshallers.
func (p *PooledMarshaller) Marshal(obj interface{}) ([]byte, error) {
	m := p.pool.Get().(*Marshaller)
	bts, err := m.Marshal(obj)
	p.pool.Put(m)
	return bts, err
}

// Encode encodes the passed object to the given writer using the pool of
// marshallers.
func (p *PooledMarshaller) Encode(obj interface{}, w io.Writer) error {
	m := p.pool.Get().(*Marshaller)
	err := m.Encode(obj, w)
	p.pool.Put(m)
	return err
}
