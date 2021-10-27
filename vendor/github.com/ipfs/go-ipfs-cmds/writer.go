package cmds

import (
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"sync"
)

// NewWriterResponseEmitter creates a response emitter that sends responses to
// the given WriterCloser.
func NewWriterResponseEmitter(w io.WriteCloser, req *Request) (ResponseEmitter, error) {
	_, valEnc, err := GetEncoder(req, w, Undefined)
	if err != nil {
		return nil, err
	}

	re := &writerResponseEmitter{
		w:   w,
		c:   w,
		req: req,
		enc: valEnc,
	}

	return re, nil
}

// NewReaderResponse creates a Response from the given reader.
func NewReaderResponse(r io.Reader, req *Request) (Response, error) {
	encType := GetEncoding(req, Undefined)
	dec, ok := Decoders[encType]
	if !ok {
		return nil, Errorf(ErrClient, "unknown encoding: %s", encType)
	}
	return &readerResponse{
		req:     req,
		r:       r,
		encType: encType,
		dec:     dec(r),
		emitted: make(chan struct{}),
	}, nil
}

type readerResponse struct {
	r       io.Reader
	encType EncodingType
	dec     Decoder

	req *Request

	length uint64
	err    error

	emitted chan struct{}
	once    sync.Once
}

func (r *readerResponse) Request() *Request {
	return r.req
}

func (r *readerResponse) Error() *Error {
	<-r.emitted

	if err, ok := r.err.(*Error); ok {
		return err
	}

	return &Error{Message: r.err.Error()}
}

func (r *readerResponse) Length() uint64 {
	<-r.emitted

	return r.length
}

func (r *readerResponse) Next() (interface{}, error) {
	m := &MaybeError{Value: r.req.Command.Type}
	err := r.dec.Decode(m)
	if err != nil {
		return nil, err
	}

	r.once.Do(func() { close(r.emitted) })

	v, err := m.Get()

	// because working with pointers to arrays is annoying
	if t := reflect.TypeOf(v); t.Kind() == reflect.Ptr && t.Elem().Kind() == reflect.Slice {
		v = reflect.ValueOf(v).Elem().Interface()
	}
	return v, err
}

type writerResponseEmitter struct {
	// TODO maybe make those public?
	w   io.Writer
	c   io.Closer
	enc Encoder
	req *Request

	length *uint64

	emitted bool
	closed  bool
}

func (re *writerResponseEmitter) CloseWithError(err error) error {
	if re.closed {
		return ErrClosingClosedEmitter
	}

	if err == nil || err == io.EOF {
		return re.Close()
	}

	cwe, ok := re.c.(interface {
		CloseWithError(error) error
	})
	if ok {
		re.closed = true
		return cwe.CloseWithError(err)
	}

	return errors.New("provided closer does not support CloseWithError")
}

func (re *writerResponseEmitter) SetLength(length uint64) {
	if re.emitted {
		return
	}

	*re.length = length
}

func (re *writerResponseEmitter) Close() error {
	if re.closed {
		return ErrClosingClosedEmitter
	}

	re.closed = true
	return re.c.Close()
}

func (re *writerResponseEmitter) Emit(v interface{}) error {
	// channel emission iteration
	if ch, ok := v.(chan interface{}); ok {
		v = (<-chan interface{})(ch)
	}
	if ch, isChan := v.(<-chan interface{}); isChan {
		return EmitChan(re, ch)
	}

	if re.closed {
		return ErrClosedEmitter
	}

	re.emitted = true

	var isSingle bool
	if s, ok := v.(Single); ok {
		v = s.Value
		isSingle = true
	}

	err := re.enc.Encode(v)
	if err != nil {
		return err
	}

	if isSingle {
		return re.Close()
	}

	return nil
}

type MaybeError struct {
	Value interface{} // needs to be a pointer
	Error *Error

	isError bool
}

func (m *MaybeError) Get() (interface{}, error) {
	if m.isError {
		return nil, m.Error
	}
	return m.Value, nil
}

func (m *MaybeError) UnmarshalJSON(data []byte) error {
	var e Error
	err := json.Unmarshal(data, &e)
	if err == nil {
		m.isError = true
		m.Error = &e
		return nil
	}

	if m.Value != nil {
		// make sure we are working with a pointer here
		v := reflect.ValueOf(m.Value)
		if v.Kind() != reflect.Ptr {
			m.Value = reflect.New(v.Type()).Interface()
		}

		err = json.Unmarshal(data, m.Value)
	} else {
		// let the json decoder decode into whatever it finds appropriate
		err = json.Unmarshal(data, &m.Value)
	}

	return err
}
