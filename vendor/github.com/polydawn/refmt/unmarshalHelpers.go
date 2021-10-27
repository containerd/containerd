package refmt

import (
	"io"

	"github.com/polydawn/refmt/cbor"
	"github.com/polydawn/refmt/json"
	"github.com/polydawn/refmt/obj/atlas"
)

type DecodeOptions interface {
	IsDecodeOptions() // marker method.
}

func Unmarshal(opts DecodeOptions, data []byte, v interface{}) error {
	switch o2 := opts.(type) {
	case json.DecodeOptions:
		return json.Unmarshal(data, v)
	case cbor.DecodeOptions:
		return cbor.Unmarshal(o2, data, v)
	default:
		panic("incorrect usage: unknown DecodeOptions type")
	}
}

func UnmarshalAtlased(opts DecodeOptions, data []byte, v interface{}, atl atlas.Atlas) error {
	switch o2 := opts.(type) {
	case json.DecodeOptions:
		return json.UnmarshalAtlased(data, v, atl)
	case cbor.DecodeOptions:
		return cbor.UnmarshalAtlased(o2, data, v, atl)
	default:
		panic("incorrect usage: unknown DecodeOptions type")
	}
}

type Unmarshaller interface {
	Unmarshal(v interface{}) error
}

func NewUnmarshaller(opts DecodeOptions, r io.Reader) Unmarshaller {
	switch o2 := opts.(type) {
	case json.DecodeOptions:
		return json.NewUnmarshaller(r)
	case cbor.DecodeOptions:
		return cbor.NewUnmarshaller(o2, r)
	default:
		panic("incorrect usage: unknown DecodeOptions type")
	}
}

func NewUnmarshallerAtlased(opts DecodeOptions, r io.Reader, atl atlas.Atlas) Unmarshaller {
	switch o2 := opts.(type) {
	case json.DecodeOptions:
		return json.NewUnmarshallerAtlased(r, atl)
	case cbor.DecodeOptions:
		return cbor.NewUnmarshallerAtlased(o2, r, atl)
	default:
		panic("incorrect usage: unknown DecodeOptions type")
	}
}
