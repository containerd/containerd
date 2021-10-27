package cmds

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"reflect"
)

// Encoder encodes values onto e.g. an io.Writer. Examples are json.Encoder and xml.Encoder.
type Encoder interface {
	Encode(value interface{}) error
}

// Decoder decodes values into value (which should be a pointer).
type Decoder interface {
	Decode(value interface{}) error
}

// EncodingType defines a supported encoding
type EncodingType string

// PostRunType defines which PostRunFunc should be used
type PostRunType string

// Supported EncodingType constants.
const (
	// General purpose
	Undefined = ""

	// EncodingTypes
	JSON        = "json"
	XML         = "xml"
	Protobuf    = "protobuf"
	Text        = "text"
	TextNewline = "textnl"

	// PostRunTypes
	CLI = "cli"
)

var Decoders = map[EncodingType]func(w io.Reader) Decoder{
	XML: func(r io.Reader) Decoder {
		return xml.NewDecoder(r)
	},
	JSON: func(r io.Reader) Decoder {
		return json.NewDecoder(r)
	},
}

type EncoderFunc func(req *Request) func(w io.Writer) Encoder
type EncoderMap map[EncodingType]EncoderFunc

var Encoders = EncoderMap{
	XML: func(req *Request) func(io.Writer) Encoder {
		return func(w io.Writer) Encoder { return xml.NewEncoder(w) }
	},
	JSON: func(req *Request) func(io.Writer) Encoder {
		return func(w io.Writer) Encoder { return json.NewEncoder(w) }
	},
	Text: func(req *Request) func(io.Writer) Encoder {
		return func(w io.Writer) Encoder { return TextEncoder{w: w} }
	},
	TextNewline: func(req *Request) func(io.Writer) Encoder {
		return func(w io.Writer) Encoder { return TextEncoder{w: w, suffix: "\n"} }
	},
}

func MakeEncoder(f func(*Request, io.Writer, interface{}) error) func(*Request) func(io.Writer) Encoder {
	return func(req *Request) func(io.Writer) Encoder {
		return func(w io.Writer) Encoder { return &genericEncoder{f: f, w: w, req: req} }
	}
}

func MakeTypedEncoder(f interface{}) func(*Request) func(io.Writer) Encoder {
	val := reflect.ValueOf(f)
	t := val.Type()
	if t.Kind() != reflect.Func || t.NumIn() != 3 {
		panic("MakeTypedEncoder must receive a function with three parameters")
	}

	errorInterface := reflect.TypeOf((*error)(nil)).Elem()
	if t.NumOut() != 1 || !t.Out(0).Implements(errorInterface) {
		panic("MakeTypedEncoder must return an error")
	}

	writerInt := reflect.TypeOf((*io.Writer)(nil)).Elem()
	if t.In(0) != reflect.TypeOf(&Request{}) || !t.In(1).Implements(writerInt) {
		panic("MakeTypedEncoder must receive a function matching func(*Request, io.Writer, ...)")
	}

	var (
		valType, valTypeAlt reflect.Type
	)

	valType = t.In(2)
	valTypeIsPtr := valType.Kind() == reflect.Ptr
	if valTypeIsPtr {
		valTypeAlt = valType.Elem()
	} else {
		valTypeAlt = reflect.PtrTo(valType)
	}

	return MakeEncoder(func(req *Request, w io.Writer, i interface{}) error {
		iType := reflect.TypeOf(i)
		iValue := reflect.ValueOf(i)
		switch iType {
		case valType:
		case valTypeAlt:
			if valTypeIsPtr {
				if iValue.CanAddr() {
					iValue = iValue.Addr()
				} else {
					oldValue := iValue
					iValue = reflect.New(iType)
					iValue.Elem().Set(oldValue)
				}
			} else {
				iValue = iValue.Elem()
			}
		default:
			return fmt.Errorf("unexpected type %T, expected %v", i, valType)
		}

		out := val.Call([]reflect.Value{
			reflect.ValueOf(req),
			reflect.ValueOf(w),
			iValue,
		})

		err, ok := out[0].Interface().(error)
		if ok {
			return err
		}
		return nil
	})
}

type genericEncoder struct {
	f   func(*Request, io.Writer, interface{}) error
	w   io.Writer
	req *Request
}

func (e *genericEncoder) Encode(v interface{}) error {
	return e.f(e.req, e.w, v)
}

type TextEncoder struct {
	w      io.Writer
	suffix string
}

func (e TextEncoder) Encode(v interface{}) error {
	_, err := fmt.Fprintf(e.w, "%s%s", v, e.suffix)
	return err
}

// GetEncoder takes a request and returns returns the encoding type and the encoder.
func GetEncoder(req *Request, w io.Writer, def EncodingType) (encType EncodingType, enc Encoder, err error) {
	encType = GetEncoding(req, def)

	var (
		fn EncoderFunc
		ok bool
	)
	if req.Command != nil {
		fn, ok = req.Command.Encoders[encType]
	}
	if !ok {
		fn, ok = Encoders[encType]
	}
	if !ok {
		return encType, nil, Errorf(ErrClient, "invalid encoding: %s", encType)
	}
	return encType, fn(req)(w), nil
}
