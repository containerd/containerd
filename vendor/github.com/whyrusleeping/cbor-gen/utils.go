package typegen

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"io/ioutil"
	"math"

	cid "github.com/ipfs/go-cid"
)

func ScanForLinks(br io.Reader) ([]cid.Cid, error) {
	var out []cid.Cid
	if err := scanForLinksRec(br, func(c cid.Cid) {
		out = append(out, c)
	}); err != nil {
		return nil, err
	}

	return out, nil
}

func scanForLinksRec(br io.Reader, cb func(cid.Cid)) error {
	maj, extra, err := CborReadHeader(br)
	if err != nil {
		return err
	}

	switch maj {
	case MajUnsignedInt, MajNegativeInt, MajOther:
	case MajByteString, MajTextString:
		_, err := io.CopyN(ioutil.Discard, br, int64(extra))
		if err != nil {
			return err
		}
	case MajTag:
		if extra == 42 {
			buf, err := ReadByteArray(br, 1000)
			if err != nil {
				return err
			}
			c, err := cid.Cast(buf[1:])
			if err != nil {
				return err
			}
			cb(c)
		} else {
			if err := scanForLinksRec(br, cb); err != nil {
				return err
			}
		}
	case MajArray:
		for i := 0; i < int(extra); i++ {
			if err := scanForLinksRec(br, cb); err != nil {
				return err
			}
		}
	case MajMap:
		for i := 0; i < int(extra*2); i++ {
			if err := scanForLinksRec(br, cb); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unhandled cbor type: %d", maj)
	}
	return nil
}

const (
	MajUnsignedInt = 0
	MajNegativeInt = 1
	MajByteString  = 2
	MajTextString  = 3
	MajArray       = 4
	MajMap         = 5
	MajTag         = 6
	MajOther       = 7
)

type CBORUnmarshaler interface {
	UnmarshalCBOR(io.Reader) error
}

type CBORMarshaler interface {
	MarshalCBOR(io.Writer) error
}

type Deferred struct {
	Raw []byte
}

func (d *Deferred) MarshalCBOR(w io.Writer) error {
	_, err := w.Write(d.Raw)
	return err
}

func (d *Deferred) UnmarshalCBOR(br io.Reader) error {
	// TODO: theres a more efficient way to implement this method, but for now
	// this is fine
	maj, extra, err := CborReadHeader(br)
	if err != nil {
		return err
	}
	header := CborEncodeMajorType(maj, extra)

	switch maj {
	case MajUnsignedInt, MajNegativeInt, MajOther:
		d.Raw = header
		return nil
	case MajByteString, MajTextString:
		buf := make([]byte, int(extra)+len(header))
		copy(buf, header)
		if _, err := io.ReadFull(br, buf[len(header):]); err != nil {
			return err
		}

		d.Raw = buf

		return nil
	case MajTag:
		sub := new(Deferred)
		if err := sub.UnmarshalCBOR(br); err != nil {
			return err
		}

		d.Raw = append(header, sub.Raw...)
		return nil
	case MajArray:
		d.Raw = header
		for i := 0; i < int(extra); i++ {
			sub := new(Deferred)
			if err := sub.UnmarshalCBOR(br); err != nil {
				return err
			}

			d.Raw = append(d.Raw, sub.Raw...)
		}
		return nil
	case MajMap:
		d.Raw = header
		sub := new(Deferred)
		for i := 0; i < int(extra*2); i++ {
			sub.Raw = sub.Raw[:0]
			if err := sub.UnmarshalCBOR(br); err != nil {
				return err
			}
			d.Raw = append(d.Raw, sub.Raw...)
		}
		return nil
	default:
		return fmt.Errorf("unhandled deferred cbor type: %d", maj)
	}
}

// this is a bit gnarly i should just switch to taking in a byte array at the top level
type BytePeeker interface {
	io.Reader
	PeekByte() (byte, error)
}

type peeker struct {
	io.Reader
}

func (p *peeker) PeekByte() (byte, error) {
	switch r := p.Reader.(type) {
	case *bytes.Reader:
		b, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		return b, r.UnreadByte()
	case *bytes.Buffer:
		b, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		return b, r.UnreadByte()
	case *bufio.Reader:
		o, err := r.Peek(1)
		if err != nil {
			return 0, err
		}

		return o[0], nil
	default:
		panic("invariant violated")
	}
}

func GetPeeker(r io.Reader) BytePeeker {
	switch r := r.(type) {
	case *bytes.Reader:
		return &peeker{r}
	case *bytes.Buffer:
		return &peeker{r}
	case *bufio.Reader:
		return &peeker{r}
	case *peeker:
		return r
	default:
		return &peeker{bufio.NewReaderSize(r, 16)}
	}
}

func readByte(r io.Reader) (byte, error) {
	if br, ok := r.(io.ByteReader); ok {
		return br.ReadByte()
	}
	var b [1]byte
	_, err := io.ReadFull(r, b[:])
	return b[0], err
}

func CborReadHeader(br io.Reader) (byte, uint64, error) {
	first, err := readByte(br)
	if err != nil {
		return 0, 0, err
	}

	maj := (first & 0xe0) >> 5
	low := first & 0x1f

	switch {
	case low < 24:
		return maj, uint64(low), nil
	case low == 24:
		next, err := readByte(br)
		if err != nil {
			return 0, 0, err
		}
		if next < 24 {
			return 0, 0, fmt.Errorf("cbor input was not canonical (lval 24 with value < 24)")
		}
		return maj, uint64(next), nil
	case low == 25:
		buf := make([]byte, 2)
		if _, err := io.ReadFull(br, buf); err != nil {
			return 0, 0, err
		}
		val := uint64(binary.BigEndian.Uint16(buf))
		if val <= math.MaxUint8 {
			return 0, 0, fmt.Errorf("cbor input was not canonical (lval 25 with value <= MaxUint8)")
		}
		return maj, val, nil
	case low == 26:
		buf := make([]byte, 4)
		if _, err := io.ReadFull(br, buf); err != nil {
			return 0, 0, err
		}
		val := uint64(binary.BigEndian.Uint32(buf))
		if val <= math.MaxUint16 {
			return 0, 0, fmt.Errorf("cbor input was not canonical (lval 26 with value <= MaxUint16)")
		}
		return maj, val, nil
	case low == 27:
		buf := make([]byte, 8)
		if _, err := io.ReadFull(br, buf); err != nil {
			return 0, 0, err
		}
		val := binary.BigEndian.Uint64(buf)
		if val <= math.MaxUint32 {
			return 0, 0, fmt.Errorf("cbor input was not canonical (lval 27 with value <= MaxUint32)")
		}
		return maj, val, nil
	default:
		return 0, 0, fmt.Errorf("invalid header: (%x)", first)
	}
}

func CborWriteHeader(w io.Writer, t byte, val uint64) error {
	header := CborEncodeMajorType(t, val)
	if _, err := w.Write(header); err != nil {
		return err
	}

	return nil
}

func CborEncodeMajorType(t byte, l uint64) []byte {
	var b [9]byte
	switch {
	case l < 24:
		b[0] = (t << 5) | byte(l)
		return b[:1]
	case l < (1 << 8):
		b[0] = (t << 5) | 24
		b[1] = byte(l)
		return b[:2]
	case l < (1 << 16):
		b[0] = (t << 5) | 25
		binary.BigEndian.PutUint16(b[1:3], uint16(l))
		return b[:3]
	case l < (1 << 32):
		b[0] = (t << 5) | 26
		binary.BigEndian.PutUint32(b[1:5], uint32(l))
		return b[:5]
	default:
		b[0] = (t << 5) | 27
		binary.BigEndian.PutUint64(b[1:], uint64(l))
		return b[:]
	}
}

func ReadTaggedByteArray(br io.Reader, exptag uint64, maxlen uint64) ([]byte, error) {
	maj, extra, err := CborReadHeader(br)
	if err != nil {
		return nil, err
	}

	if maj != MajTag {
		return nil, fmt.Errorf("expected cbor type 'tag' in input")
	}

	if extra != exptag {
		return nil, fmt.Errorf("expected tag %d", exptag)
	}

	return ReadByteArray(br, maxlen)
}

func ReadByteArray(br io.Reader, maxlen uint64) ([]byte, error) {
	maj, extra, err := CborReadHeader(br)
	if err != nil {
		return nil, err
	}

	if maj != MajByteString {
		return nil, fmt.Errorf("expected cbor type 'byte string' in input")
	}

	if extra > 256*1024 {
		return nil, fmt.Errorf("string in cbor input too long")
	}

	buf := make([]byte, extra)
	if _, err := io.ReadFull(br, buf); err != nil {
		return nil, err
	}

	return buf, nil
}

var (
	CborBoolFalse = []byte{0xf4}
	CborBoolTrue  = []byte{0xf5}
	CborNull      = []byte{0xf6}
)

func EncodeBool(b bool) []byte {
	if b {
		return []byte{0xf5}
	}
	return []byte{0xf4}
}

func WriteBool(w io.Writer, b bool) error {
	_, err := w.Write(EncodeBool(b))
	return err
}

func ReadString(r io.Reader) (string, error) {
	maj, l, err := CborReadHeader(r)
	if err != nil {
		return "", err
	}

	if maj != MajTextString {
		return "", fmt.Errorf("got tag %d while reading string value (l = %d)", maj, l)
	}

	if l > MaxLength {
		return "", fmt.Errorf("string in input was too long")
	}

	buf := make([]byte, l)
	_, err = io.ReadFull(r, buf)
	if err != nil {
		return "", err
	}

	return string(buf), nil
}

func ReadCid(br io.Reader) (cid.Cid, error) {
	buf, err := ReadTaggedByteArray(br, 42, 512)
	if err != nil {
		return cid.Undef, err
	}

	return bufToCid(buf)
}

func bufToCid(buf []byte) (cid.Cid, error) {

	if len(buf) == 0 {
		return cid.Undef, fmt.Errorf("undefined cid")
	}

	if len(buf) < 2 {
		return cid.Undef, fmt.Errorf("cbor serialized CIDs must have at least two bytes")
	}

	if buf[0] != 0 {
		return cid.Undef, fmt.Errorf("cbor serialized CIDs must have binary multibase")
	}

	return cid.Cast(buf[1:])
}

func WriteCid(w io.Writer, c cid.Cid) error {
	if err := CborWriteHeader(w, MajTag, 42); err != nil {
		return err
	}
	if c == cid.Undef {
		return fmt.Errorf("undefined cid")
		//return CborWriteHeader(w, MajByteString, 0)
	}

	if err := CborWriteHeader(w, MajByteString, uint64(len(c.Bytes())+1)); err != nil {
		return err
	}

	// that binary multibase prefix...
	if _, err := w.Write([]byte{0}); err != nil {
		return err
	}

	if _, err := w.Write(c.Bytes()); err != nil {
		return err
	}

	return nil
}

type CborBool bool

func (cb *CborBool) MarshalCBOR(w io.Writer) error {
	return WriteBool(w, bool(*cb))
}

func (cb *CborBool) UnmarshalCBOR(r io.Reader) error {
	t, val, err := CborReadHeader(r)
	if err != nil {
		return err
	}

	if t != MajOther {
		return fmt.Errorf("booleans should be major type 7")
	}

	switch val {
	case 20:
		*cb = false
	case 21:
		*cb = true
	default:
		return fmt.Errorf("booleans are either major type 7, value 20 or 21 (got %d)", val)
	}
	return nil
}

type CborInt int64

func (ci *CborInt) MarshalCBOR(w io.Writer) error {
	v := int64(*ci)
	if v >= 0 {
		if _, err := w.Write(CborEncodeMajorType(MajUnsignedInt, uint64(v))); err != nil {
			return err
		}
	} else {
		if _, err := w.Write(CborEncodeMajorType(MajNegativeInt, uint64(-v)-1)); err != nil {
			return err
		}
	}
	return nil
}

func (ci *CborInt) UnmarshalCBOR(r io.Reader) error {
	maj, extra, err := CborReadHeader(r)
	if err != nil {
		return err
	}
	var extraI int64
	switch maj {
	case MajUnsignedInt:
		extraI = int64(extra)
		if extraI < 0 {
			return fmt.Errorf("int64 positive overflow")
		}
	case MajNegativeInt:
		extraI = int64(extra)
		if extraI < 0 {
			return fmt.Errorf("int64 negative oveflow")
		}
		extraI = -1 - extraI
	default:
		return fmt.Errorf("wrong type for int64 field: %d", maj)
	}

	*ci = CborInt(extraI)
	return nil
}
