package cbor

import (
	"fmt"
	"io"
)

var (
	_ quickWriter = &quickWriterStream{}
)

// quickWriter is implements several methods that are specificly useful to the performance
// needs of our encoders, and abstracts writing to a byte array or to an io.Writer.
type quickWriter interface {
	writeb([]byte)
	writestr(string)
	writen1(byte)
	writen2(byte, byte)
	checkErr() error
	clearErr()
}

// Interface used to detect if efficient string writing is supported.
// Same as in stdlib 'io' pkg; also not exported there, so we declare it again ourselves.
type stringWriter interface {
	WriteString(s string) (n int, err error)
}

// quickWriterStream is a quickWriter that routes bytes to an io.Writer.
// While this implementation does use some internal buffers, it's still advisable
// to use a buffered writer to avoid small operations for any external IO like disk or network.
type quickWriterStream struct {
	w  io.Writer
	ws stringWriter // nil if not available

	scratch  [2]byte
	scratch1 []byte
	scratch2 []byte
	err      error
}

func newQuickWriterStream(w io.Writer) *quickWriterStream {
	z := &quickWriterStream{w: w}
	if ws, ok := w.(stringWriter); ok {
		z.ws = ws
	}
	z.scratch1 = z.scratch[:1]
	z.scratch2 = z.scratch[:2]
	return z
}

func (z *quickWriterStream) writeb(bs []byte) {
	n, err := z.w.Write(bs)
	if err != nil && z.err == nil {
		z.err = err
	}
	if n < len(bs) && z.err == nil {
		z.err = fmt.Errorf("underwrite")
	}
}

func (z *quickWriterStream) writestr(s string) {
	var n int
	var err error
	if z.ws != nil {
		n, err = z.ws.WriteString(s)
	} else {
		n, err = z.w.Write([]byte(s)) // Notice: alloc!
	}
	if err != nil && z.err == nil {
		z.err = err
	}
	if n < len(s) && z.err == nil {
		z.err = fmt.Errorf("underwrite")
	}
}

func (z *quickWriterStream) writen1(b byte) {
	z.scratch1[0] = b
	n, err := z.w.Write(z.scratch1)
	if err != nil && z.err == nil {
		z.err = err
	}
	if n < 1 && z.err == nil {
		z.err = fmt.Errorf("underwrite")
	}
}

func (z *quickWriterStream) writen2(b1 byte, b2 byte) {
	z.scratch2[0] = b1
	z.scratch2[1] = b2
	n, err := z.w.Write(z.scratch2)
	if err != nil && z.err == nil {
		z.err = err
	}
	if n < 2 && z.err == nil {
		z.err = fmt.Errorf("underwrite")
	}
}

func (z *quickWriterStream) checkErr() error {
	return z.err
}

func (z *quickWriterStream) clearErr() {
	z.err = nil
}
