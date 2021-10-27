package cbor

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

const (
	maxUint = ^uint(0)
	maxInt  = int(maxUint >> 1)
)

func (d *Decoder) decodeFloat(majorByte byte) (f float64, err error) {
	var bs []byte
	switch majorByte {
	case cborSigilFloat16:
		bs, err = d.r.Readnzc(2)
		f = float64(math.Float32frombits(halfFloatToFloatBits(binary.BigEndian.Uint16(bs))))
	case cborSigilFloat32:
		bs, err = d.r.Readnzc(4)
		f = float64(math.Float32frombits(binary.BigEndian.Uint32(bs)))
	case cborSigilFloat64:
		bs, err = d.r.Readnzc(8)
		f = math.Float64frombits(binary.BigEndian.Uint64(bs))
	}
	return
}

// Decode an unsigned int.
// Must continue to hand down the majorByte because some of its bits are either
// packed with the value outright, or tell us how many more bytes the value fills.
func (d *Decoder) decodeUint(majorByte byte) (ui uint64, err error) {
	v := majorByte & 0x1f
	if v <= 0x17 {
		ui = uint64(v)
	} else {
		if v == 0x18 {
			var b byte
			b, err = d.r.Readn1()
			ui = uint64(b)
		} else if v == 0x19 {
			var bs []byte
			bs, err = d.r.Readnzc(2)
			ui = uint64(binary.BigEndian.Uint16(bs))
		} else if v == 0x1a {
			var bs []byte
			bs, err = d.r.Readnzc(4)
			ui = uint64(binary.BigEndian.Uint32(bs))
		} else if v == 0x1b {
			var bs []byte
			bs, err = d.r.Readnzc(8)
			ui = uint64(binary.BigEndian.Uint64(bs))
		} else {
			err = fmt.Errorf("decodeUint: Invalid descriptor: %v", majorByte)
			return
		}
	}
	return
}

// Decode a *negative* integer.
// Note that CBOR has a very funny-shaped hole here: there is unsigned positive int,
// and there is explicitly negative signed int... and there is no signed, positive int.
// *We have no 'decodeInt' function because that **doesn't exist** in CBOR.*
// So!  Hopefully our consumer doesn't mind having to cast uints to ints fairly frequently.
func (d *Decoder) decodeNegInt(majorByte byte) (i int64, err error) {
	// The packed bits in the majorByte and the following bytes if any are layed out
	// the exact same as a uint; only the major type bits are different.
	ui, err := d.decodeUint(majorByte)
	if err != nil {
		return 0, err
	}
	pos := ui + 1
	if pos > uint64(-math.MinInt64) {
		return -1, errors.New("cbor: negative integer out of rage of int64 type")
	}

	return -int64(pos), nil
}

// Decode expecting a positive integer.
// None of our token-yielding functions call this directly;
// it's used inside the library when we expect to read e.g. a length header.
// Does not check that your majorByte indicates an integer type at all;
// in context, it often doesn't, e.g. when decoding the length of a string.
func (d *Decoder) decodeLen(majorByte byte) (i int, err error) {
	ui, err := d.decodeUint(majorByte)
	if err != nil {
		return 0, err
	}
	if ui > uint64(maxInt) {
		return 0, errors.New("cbor: positive integer is out of length")
	}
	return int(ui), nil
}

// Decoding indefinite-length byte strings in cbor is actually decoding a sequence of
// definite-length byte strings until you encounter a break.
// Caller: use `bs[:0]` if you have something to reuse, or nil
func (d *Decoder) decodeBytesIndefinite(bs []byte) (bsOut []byte, err error) {
	return d.decodeBytesOrStringIndefinite(bs, cborMajorBytes)
}

func (d *Decoder) decodeStringIndefinite() (s string, err error) {
	bs, err := d.decodeBytesOrStringIndefinite(nil, cborMajorString)
	if err != nil {
		return "", err
	}
	return string(bs), nil
}

func (d *Decoder) decodeBytesOrStringIndefinite(bs []byte, majorWanted byte) (bsOut []byte, err error) {
	if bs == nil {
		bs = make([]byte, 0, 16)
	}
	var n int
	for {
		// Read first byte; check for break, or hunk, or invalid.
		// (It's not necessary to have the first majorByte as a param to this function, because
		// indefinite length sequences have a separate sigil which doesn't pack any len info.)
		majorByte, err := d.r.Readn1()
		if err != nil {
			return bs, err
		}
		if majorByte == cborSigilBreak {
			return bs, nil
		} else if major := majorByte | 0x1f - 0x1f; major != majorWanted {
			return bs, fmt.Errorf("cbor: expect bytes or string major type in indefinite string/bytes; got: %v, byte: %v", major, majorByte)
		}
		// Read length header for this hunk, and ensure we have at least that much cap.
		n, err = d.decodeLen(majorByte)
		if err != nil {
			return bs, err
		}
		oldLen := len(bs)
		newLen := oldLen + n
		if n > 33554432 {
			return nil, fmt.Errorf("cbor: decoding rejected oversized indefinite string/bytes field: %d is too large", n)
		}
		if newLen > cap(bs) {
			bs2 := make([]byte, newLen, 2*cap(bs)+n)
			copy(bs2, bs)
			bs = bs2
		} else {
			bs = bs[:newLen]
		}
		// Read that hunk.
		d.r.Readb(bs[oldLen:newLen])
	}
}

// Decode a single length-prefixed hunk of bytes.
//
// There are a number of ways this may try to conserve allocations:
//
// - If you say zerocopy=true, and the underlying reader system already has an
//  appropriate byte slice available, then a slice from that will be returned.
//
// - If you provide a byte slice, we will attempt to use it.
//  The byte slice is truncated and used for its capacity only -- not appended.
//  The final returned slice may be a different one if the provided slice did not
//  have sufficient capacity.
//
// - If you say zerocopy=true, and the underlying read system doesn't have an
//  efficient way to yield a slice of its internal buffer, and you provided no
//  destination slice, then we will use a recycleable piece of memory in the Decoder
//  state and return a slice viewing into it.  For small values this will
//  likely save an alloc.
//
// The above rules are resolved in this order; e.g. your byte slice is disregarded
// if zerocopy=true and the underlying reader can do something even more efficient,
// though there is also no harm to providing the slice argument.
// Generally, set zerocopy if you know you're not going to publicly yield the results,
// and the implementation will do its best to be as efficient as possible.
//
// Zerocopy is appropriate when planning to turn the bytes into a string, for example,
// since in that path we know the slice will be treated immutably, not publicly
// exposed, and also any other copy to another intermediate is definitely useless.
func (d *Decoder) decodeBytes(majorByte byte) (bs []byte, err error) {
	n, err := d.decodeLen(majorByte)
	if err != nil {
		return nil, err
	}
	if n > 33554432 {
		return nil, fmt.Errorf("cbor: decoding rejected oversized byte field: %d is too large", n)
	}
	return d.r.Readn(n)
}

// Decode a single length-prefixed string.
func (d *Decoder) decodeString(majorByte byte) (s string, err error) {
	n, err := d.decodeLen(majorByte)
	if err != nil {
		return "", err
	}
	if n > 33554432 {
		return "", fmt.Errorf("cbor: decoding rejected oversized string field: %d is too large", n)
	}
	bs, err := d.r.Readnzc(n)
	return string(bs), err
}

// culled from OGRE (Object-Oriented Graphics Rendering Engine)
// function: halfToFloatI (http://stderr.org/doc/ogre-doc/api/OgreBitwise_8h-source.html)
func halfFloatToFloatBits(yy uint16) (d uint32) {
	y := uint32(yy)
	s := (y >> 15) & 0x01
	e := (y >> 10) & 0x1f
	m := y & 0x03ff

	if e == 0 {
		if m == 0 { // plu or minus 0
			return s << 31
		} else { // Denormalized number -- renormalize it
			for (m & 0x00000400) == 0 {
				m <<= 1
				e -= 1
			}
			e += 1
			const zz uint32 = 0x0400
			m &= ^zz
		}
	} else if e == 31 {
		if m == 0 { // Inf
			return (s << 31) | 0x7f800000
		} else { // NaN
			return (s << 31) | 0x7f800000 | (m << 13)
		}
	}
	e = e + (127 - 15)
	m = m << 13
	return (s << 31) | (e << 23) | m
}
