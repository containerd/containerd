package shared

import (
	"bytes"
	"errors"
	"io"
)

const (
	scratchByteArrayLen = 32
)

var (
	zeroByteSlice = []byte{}[:0:0]
)

var (
	_ SlickReader = &SlickReaderStream{}
	_ SlickReader = &SlickReaderSlice{}
)

func NewReader(r io.Reader) SlickReader {
	return &SlickReaderStream{br: &readerToScanner{r: r}}
}

func NewBytesReader(buf *bytes.Buffer) SlickReader {
	return &SlickReaderStream{br: buf}
}

func NewSliceReader(b []byte) SlickReader {
	return &SlickReaderSlice{b: b}
}

// SlickReader is a hybrid of reader and buffer interfaces with methods giving
// specific attention to the performance needs found in a decoder.
// Implementations cover io.Reader as well as []byte directly.
//
// In particular, it allows:
//
//   - returning byte-slices with zero-copying (you were warned!) when possible
//   - returning byte-slices for short reads which will be reused (you were warned!)
//   - putting a 'track' point in the buffer, and later yielding all those bytes at once
//   - counting the number of bytes read (for use in parser error messages, mainly)
//
// All of these shortcuts mean correct usage is essential to avoid unexpected behaviors,
// but in return allow avoiding many, many common sources of memory allocations in a parser.
type SlickReader interface {

	// Read n bytes into a byte slice which may be shared and must not be reused
	// After any additional calls to this reader.
	// Readnzc will use the implementation scratch buffer if possible,
	// i.e. n < len(scratchbuf), or may return a view of the []byte being decoded from.
	// Requesting a zero length read will return `zeroByteSlice`, a len-zero cap-zero slice.
	Readnzc(n int) ([]byte, error)

	// Read n bytes into a new byte slice.
	// If zero-copy views into existing buffers are acceptable (e.g. you know you
	// won't later mutate, reference or expose this memory again), prefer `Readnzc`.
	// If you already have an existing slice of sufficient size to reuse, prefer `Readb`.
	// Requesting a zero length read will return `zeroByteSlice`, a len-zero cap-zero slice.
	Readn(n int) ([]byte, error)

	// Read `len(b)` bytes into the given slice, starting at its beginning,
	// overwriting all values, and disregarding any extra capacity.
	Readb(b []byte) error

	Readn1() (uint8, error)
	Unreadn1()
	NumRead() int // number of bytes read
	Track()
	StopTrack() []byte
}

// SlickReaderStream is a SlickReader that reads off an io.Reader.
// Initialize it by wrapping an ioDecByteScanner around your io.Reader and dumping it in.
// While this implementation does use some internal buffers, it's still advisable
// to use a buffered reader to avoid small reads for any external IO like disk or network.
type SlickReaderStream struct {
	br         readerScanner
	scratch    [scratchByteArrayLen]byte // temp byte array re-used internally for efficiency during read.
	n          int                       // num read
	tracking   []byte                    // tracking bytes read
	isTracking bool
}

func (z *SlickReaderStream) NumRead() int {
	return z.n
}

func (z *SlickReaderStream) Readnzc(n int) (bs []byte, err error) {
	if n == 0 {
		return zeroByteSlice, nil
	}
	if n < len(z.scratch) {
		bs = z.scratch[:n]
	} else {
		bs = make([]byte, n)
	}
	err = z.Readb(bs)
	return
}

func (z *SlickReaderStream) Readn(n int) (bs []byte, err error) {
	if n == 0 {
		return zeroByteSlice, nil
	}
	bs = make([]byte, n)
	err = z.Readb(bs)
	return
}

func (z *SlickReaderStream) Readb(bs []byte) error {
	if len(bs) == 0 {
		return nil
	}
	n, err := io.ReadAtLeast(z.br, bs, len(bs))
	z.n += n
	if z.isTracking {
		z.tracking = append(z.tracking, bs...)
	}
	return err
}

func (z *SlickReaderStream) Readn1() (b uint8, err error) {
	b, err = z.br.ReadByte()
	if err != nil {
		return
	}
	z.n++
	if z.isTracking {
		z.tracking = append(z.tracking, b)
	}
	return
}

func (z *SlickReaderStream) Unreadn1() {
	err := z.br.UnreadByte()
	if err != nil {
		panic(err)
	}
	z.n--
	if z.isTracking {
		if l := len(z.tracking) - 1; l >= 0 {
			z.tracking = z.tracking[:l]
		}
	}
}

func (z *SlickReaderStream) Track() {
	if z.tracking != nil {
		z.tracking = z.tracking[:0]
	}
	z.isTracking = true
}

func (z *SlickReaderStream) StopTrack() (bs []byte) {
	z.isTracking = false
	return z.tracking
}

// SlickReaderSlice implements SlickReader by reading a byte slice directly.
// Often this means the zero-copy methods can simply return subslices.
type SlickReaderSlice struct {
	b []byte // data
	c int    // cursor
	a int    // available
	t int    // track start
}

func (z *SlickReaderSlice) reset(in []byte) {
	z.b = in
	z.a = len(in)
	z.c = 0
	z.t = 0
}

func (z *SlickReaderSlice) NumRead() int {
	return z.c
}

func (z *SlickReaderSlice) Unreadn1() {
	if z.c == 0 || len(z.b) == 0 {
		panic(errors.New("cannot unread last byte read"))
	}
	z.c--
	z.a++
	return
}

func (z *SlickReaderSlice) Readnzc(n int) (bs []byte, err error) {
	if n == 0 {
		return zeroByteSlice, nil
	} else if z.a == 0 {
		return zeroByteSlice, io.EOF
	} else if n > z.a {
		return zeroByteSlice, io.ErrUnexpectedEOF
	} else {
		c0 := z.c
		z.c = c0 + n
		z.a = z.a - n
		bs = z.b[c0:z.c]
	}
	return
}

func (z *SlickReaderSlice) Readn(n int) (bs []byte, err error) {
	if n == 0 {
		return zeroByteSlice, nil
	}
	bs = make([]byte, n)
	err = z.Readb(bs)
	return
}

func (z *SlickReaderSlice) Readn1() (v uint8, err error) {
	if z.a == 0 {
		panic(io.EOF)
	}
	v = z.b[z.c]
	z.c++
	z.a--
	return
}

func (z *SlickReaderSlice) Readb(bs []byte) error {
	bs2, err := z.Readnzc(len(bs))
	copy(bs, bs2)
	return err
}

func (z *SlickReaderSlice) Track() {
	z.t = z.c
}

func (z *SlickReaderSlice) StopTrack() (bs []byte) {
	return z.b[z.t:z.c]
}

// conjoin the io.Reader and io.ByteScanner interfaces.
type readerScanner interface {
	io.Reader
	io.ByteScanner
}

// readerToScanner decorates an `io.Reader` with all the methods to also
// fulfill the `io.ByteScanner` interface.
type readerToScanner struct {
	r  io.Reader
	l  byte    // last byte
	ls byte    // last byte status. 0: init-canDoNothing, 1: canRead, 2: canUnread
	b  [1]byte // tiny buffer for reading single bytes
}

func (z *readerToScanner) Read(p []byte) (n int, err error) {
	var firstByte bool
	if z.ls == 1 {
		z.ls = 2
		p[0] = z.l
		if len(p) == 1 {
			n = 1
			return
		}
		firstByte = true
		p = p[1:]
	}
	n, err = z.r.Read(p)
	if n > 0 {
		if err == io.EOF && n == len(p) {
			err = nil // read was successful, so postpone EOF (till next time)
		}
		z.l = p[n-1]
		z.ls = 2
	}
	if firstByte {
		n++
	}
	return
}

func (z *readerToScanner) ReadByte() (c byte, err error) {
	n, err := z.Read(z.b[:])
	if n == 1 {
		c = z.b[0]
		if err == io.EOF {
			err = nil // read was successful, so postpone EOF (till next time)
		}
	}
	return
}

func (z *readerToScanner) UnreadByte() (err error) {
	x := z.ls
	if x == 0 {
		err = errors.New("cannot unread - nothing has been read")
	} else if x == 1 {
		err = errors.New("cannot unread - last byte has not been read")
	} else if x == 2 {
		z.ls = 1
	}
	return
}
