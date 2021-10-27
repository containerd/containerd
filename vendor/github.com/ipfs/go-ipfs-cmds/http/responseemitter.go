package http

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"

	cmds "github.com/ipfs/go-ipfs-cmds"
)

var (
	// AllowedExposedHeadersArr defines the default Access-Control-Expose-Headers.
	AllowedExposedHeadersArr = []string{streamHeader, channelHeader, extraContentLengthHeader}
	// AllowedExposedHeaders is the list of defaults Access-Control-Expose-Headers separated by comma.
	AllowedExposedHeaders = strings.Join(AllowedExposedHeadersArr, ", ")

	mimeTypes = map[cmds.EncodingType]string{
		cmds.Protobuf: "application/protobuf",
		cmds.JSON:     "application/json",
		cmds.XML:      "application/xml",
		cmds.Text:     "text/plain",
	}
)

// NewResponseEmitter returns a new ResponseEmitter.
func NewResponseEmitter(w http.ResponseWriter, method string, req *cmds.Request, opts ...ResponseEmitterOption) (ResponseEmitter, error) {
	encType, enc, err := cmds.GetEncoder(req, w, cmds.JSON)
	if err != nil {
		return nil, err
	}
	re := &responseEmitter{
		w:       w,
		encType: encType,
		enc:     enc,
		method:  method,
		req:     req,
	}

	// apply functional options
	for _, opt := range opts {
		opt(re)
	}

	return re, nil
}

// ResponseEmitterOption is the type describing options to the NewResponseEmitter function.
type ResponseEmitterOption func(*responseEmitter)

// withRequestBodyEOFChan return a ResponseEmitterOption needed to gracefully handle
// the case where the handler wants to send data and the request data has not been read
// completely yet.
func withRequestBodyEOFChan(ch <-chan struct{}) ResponseEmitterOption {
	return func(re *responseEmitter) {
		if ch != nil {
			re.bodyEOFChan = ch
		}
	}
}

// ResponseEmitter interface defines the components that can care of sending
// the response to HTTP Requests.
type ResponseEmitter interface {
	cmds.ResponseEmitter
	http.Flusher
}

type responseEmitter struct {
	w http.ResponseWriter

	enc     cmds.Encoder
	encType cmds.EncodingType
	req     *cmds.Request

	l      sync.Mutex
	length uint64

	bodyEOFChan <-chan struct{}

	streaming bool
	closed    bool
	once      sync.Once
	method    string
}

func (re *responseEmitter) Emit(value interface{}) error {
	// if we got a channel, instead emit values received on there.
	if ch, ok := value.(chan interface{}); ok {
		value = (<-chan interface{})(ch)
	}
	if ch, isChan := value.(<-chan interface{}); isChan {
		return cmds.EmitChan(re, ch)
	}

	re.once.Do(func() { re.preamble(value) })

	re.l.Lock()
	defer re.l.Unlock()

	if re.closed {
		return cmds.ErrClosedEmitter
	}

	var err error

	// return immediately if this is a head request
	if re.method == http.MethodHead {
		return nil
	}

	// ignore those
	if value == nil {
		return nil
	}

	var isSingle bool
	if single, ok := value.(cmds.Single); ok {
		value = single.Value
		isSingle = true
	}

	if f, ok := re.w.(http.Flusher); ok {
		defer f.Flush()
	}

	switch v := value.(type) {
	case error:
		return re.closeWithError(v)
	case io.Reader:
		err = flushCopy(re.w, v)
	default:
		err = re.enc.Encode(value)
	}

	if isSingle && err == nil {
		// only close when there were no encoding errors
		err = re.closeWithError(nil)
	}

	return err
}

func (re *responseEmitter) SetLength(l uint64) {
	re.l.Lock()
	defer re.l.Unlock()

	h := re.w.Header()
	h.Set("X-Content-Length", strconv.FormatUint(l, 10))

	re.length = l
}

func (re *responseEmitter) Close() error {
	return re.CloseWithError(nil)
}

func (re *responseEmitter) CloseWithError(err error) error {
	re.l.Lock()
	defer re.l.Unlock()

	return re.closeWithError(err)
}

func (re *responseEmitter) closeWithError(err error) error {
	if re.closed {
		return cmds.ErrClosingClosedEmitter
	}

	switch err {
	case nil:
		// no error
	case io.EOF:
		// not a real error
		err = nil
	default:
		// make sure this is *always* of type *cmds.Error
		switch e := err.(type) {
		case cmds.Error:
			err = &e
		case *cmds.Error:
		case nil:
		default:
			err = &cmds.Error{Message: err.Error(), Code: cmds.ErrNormal}
		}
	}

	setErrTrailer := true

	// use preamble directly, we're already in critical section
	// preamble needs to be before branch below, because the headers need to be written before writing the response
	re.once.Do(func() {
		re.doPreamble(err)

		// do not set error trailer if we send the error as value in preamble
		setErrTrailer = false
	})

	if setErrTrailer && err != nil {
		re.w.Header().Set(StreamErrHeader, err.Error())
	}

	re.closed = true

	return nil
}

// Flush the http connection
func (re *responseEmitter) Flush() {
	re.once.Do(func() { re.preamble(nil) })

	if flusher, ok := re.w.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (re *responseEmitter) preamble(value interface{}) {
	re.l.Lock()
	defer re.l.Unlock()

	re.doPreamble(value)
}

func (re *responseEmitter) sendErr(err *cmds.Error) {
	// Handle error encoding. *Try* to obey the requested encoding, fallback
	// on json.
	encType := re.encType
	enc, ok := cmds.Encoders[encType]
	if !ok {
		encType = cmds.JSON
		enc = cmds.Encoders[encType]
	}

	// Set the appropriate MIME Type
	re.w.Header().Set(contentTypeHeader, mimeTypes[encType])

	// Set the status from the error code.
	status := http.StatusInternalServerError
	if err.Code == cmds.ErrClient {
		status = http.StatusBadRequest
	}
	re.w.WriteHeader(status)

	// Finally, send the errr
	if err := enc(re.req)(re.w).Encode(err); err != nil {
		log.Error("error sending error value after non-200 response", err)
	}

	re.closed = true
}

func (re *responseEmitter) doPreamble(value interface{}) {
	var (
		h    = re.w.Header()
		mime string
	)

	// Common Headers

	// set 'allowed' headers
	h.Set("Access-Control-Allow-Headers", AllowedExposedHeaders)
	// expose those headers
	h.Set("Access-Control-Expose-Headers", AllowedExposedHeaders)

	// Set up our potential trailer
	h.Set("Trailer", StreamErrHeader)

	// If we have a request body, make sure we close the body
	// if we want to write before completing reading.
	// FIXME: https://github.com/ipfs/go-ipfs/issues/5168
	// FIXME: https://github.com/golang/go/issues/15527
	if re.bodyEOFChan != nil {
		select {
		case <-re.bodyEOFChan:
			// all good, we received an EOF, so the body is read completely.
			// we handle the error where it occurs, here we just want to know that we're done
		default:
			// set connection close header, because we want to write
			// even though the body is not yet read completely.
			h.Set("Connection", "close")
		}
	}

	switch v := value.(type) {
	case *cmds.Error:
		re.sendErr(v)
		return
	case io.Reader:
		// set streams output type to text to avoid issues with browsers rendering
		// html pages on priveleged api ports
		h.Set(streamHeader, "1")
		re.streaming = true

		mime = "text/plain"
	case cmds.Single:
		// don't set stream/channel header
	default:
		h.Set(channelHeader, "1")
	}

	if mime == "" {
		var ok bool

		// lookup mime type from map
		mime, ok = mimeTypes[re.encType]
		if !ok {
			// catch-all, set to text as default
			mime = "text/plain"
		}
	}

	h.Set(contentTypeHeader, mime)

	re.w.WriteHeader(http.StatusOK)
}

func flushCopy(w io.Writer, r io.Reader) error {
	buf := make([]byte, 4096)
	f, ok := w.(http.Flusher)
	if !ok {
		_, err := io.Copy(w, r)
		return err
	}
	for {
		n, err := r.Read(buf)
		switch err {
		case io.EOF:
			if n <= 0 {
				return nil
			}
			// if data was returned alongside the EOF, pretend we didnt
			// get an EOF. The next read call should also EOF.
		case nil:
			// continue
		default:
			return err
		}

		nw, err := w.Write(buf[:n])
		if err != nil {
			return err
		}

		if nw != n {
			return fmt.Errorf("http write failed to write full amount: %d != %d", nw, n)
		}

		f.Flush()
	}
}
