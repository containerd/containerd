package json

import (
	"bytes"
	"io"

	"github.com/polydawn/refmt/obj"
	"github.com/polydawn/refmt/obj/atlas"
	"github.com/polydawn/refmt/shared"
)

// All of the methods in this file are exported,
// and their names and type declarations are intended to be
// identical to the naming and types of the golang stdlib
// 'encoding/json' packages, with ONE EXCEPTION:
// what stdlib calls "NewEncoder", we call "NewMarshaller";
// what stdlib calls "NewDecoder", we call "NewUnmarshaller";
// and similarly the types and methods are "Marshaller.Marshal"
// and "Unmarshaller.Unmarshal".
// You should be able to migrate with a sed script!
//
// (In refmt, the encoder/decoder systems are for token streams;
// if you're talking about object mapping, we consistently
// refer to that as marshalling/unmarshalling.)
//
// Most methods also have an "Atlased" variant,
// which lets you specify advanced type mapping instructions.

func Marshal(v interface{}) ([]byte, error) {
	var buf bytes.Buffer
	if err := NewMarshaller(&buf).Marshal(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func MarshalAtlased(cfg EncodeOptions, v interface{}, atl atlas.Atlas) ([]byte, error) {
	var buf bytes.Buffer
	if err := NewMarshallerAtlased(&buf, cfg, atl).Marshal(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

type Marshaller struct {
	marshaller *obj.Marshaller
	encoder    *Encoder
	pump       shared.TokenPump
}

func (x *Marshaller) Marshal(v interface{}) error {
	x.marshaller.Bind(v)
	x.encoder.Reset()
	return x.pump.Run()
}

func NewMarshaller(wr io.Writer) *Marshaller {
	return NewMarshallerAtlased(wr, EncodeOptions{}, atlas.MustBuild())
}

func NewMarshallerAtlased(wr io.Writer, cfg EncodeOptions, atl atlas.Atlas) *Marshaller {
	x := &Marshaller{
		marshaller: obj.NewMarshaller(atl),
		encoder:    NewEncoder(wr, cfg),
	}
	x.pump = shared.TokenPump{
		x.marshaller,
		x.encoder,
	}
	return x
}

func Unmarshal(data []byte, v interface{}) error {
	return NewUnmarshaller(bytes.NewBuffer(data)).Unmarshal(v)
}

func UnmarshalAtlased(data []byte, v interface{}, atl atlas.Atlas) error {
	return NewUnmarshallerAtlased(bytes.NewBuffer(data), atl).Unmarshal(v)
}

type Unmarshaller struct {
	unmarshaller *obj.Unmarshaller
	decoder      *Decoder
	pump         shared.TokenPump
}

func (x *Unmarshaller) Unmarshal(v interface{}) error {
	x.unmarshaller.Bind(v)
	x.decoder.Reset()
	return x.pump.Run()
}

func NewUnmarshaller(r io.Reader) *Unmarshaller {
	return NewUnmarshallerAtlased(r, atlas.MustBuild())
}
func NewUnmarshallerAtlased(r io.Reader, atl atlas.Atlas) *Unmarshaller {
	x := &Unmarshaller{
		unmarshaller: obj.NewUnmarshaller(atl),
		decoder:      NewDecoder(r),
	}
	x.pump = shared.TokenPump{
		x.decoder,
		x.unmarshaller,
	}
	return x
}
