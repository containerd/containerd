package cbor

import (
	"io"

	. "github.com/polydawn/refmt/tok"
)

type Encoder struct {
	w quickWriter

	stack   []encoderPhase // When empty, and step returns done, all done.
	current encoderPhase   // Shortcut to end of stack.
	// Note unlike decoder, we need no statekeeping space for definite-len map and array.

	spareBytes []byte
}

func NewEncoder(w io.Writer) (d *Encoder) {
	d = &Encoder{
		w:          newQuickWriterStream(w),
		stack:      make([]encoderPhase, 0, 10),
		current:    phase_anyExpectValue,
		spareBytes: make([]byte, 8),
	}
	return
}

func (d *Encoder) Reset() {
	d.stack = d.stack[0:0]
	d.current = phase_anyExpectValue
}

type encoderPhase byte

// There's about twice as many phases that the cbor encoder can be in compared to the json encoder
// because the presense of indefinite vs definite length maps and arrays effectively adds a dimension to those.
const (
	phase_anyExpectValue           encoderPhase = iota
	phase_mapDefExpectKeyOrEnd                  // must not yield break at end
	phase_mapDefExpectValue                     // only necessary to flip back to DefExpectKey
	phase_mapIndefExpectKeyOrEnd                // must yield break at end
	phase_mapIndefExpectValue                   // only necessary to flip back to IndefExpectKey
	phase_arrDefExpectValueOrEnd                // must not yield break at end
	phase_arrIndefExpectValueOrEnd              // must yield break at end
)

func (d *Encoder) pushPhase(p encoderPhase) {
	d.current = p
	d.stack = append(d.stack, d.current)
}

// Pop a phase from the stack; return 'true' if stack now empty.
func (d *Encoder) popPhase() bool {
	n := len(d.stack) - 1
	if n == 0 {
		return true
	}
	if n < 0 { // the state machines are supposed to have already errored better
		panic("cborEncoder stack overpopped")
	}
	d.current = d.stack[n-1]
	d.stack = d.stack[0:n]
	return false
}

func (d *Encoder) Step(tokenSlot *Token) (done bool, err error) {
	/*
		Though it reads somewhat backwards from how a human would probably intuit
		cause and effect, switching on the token type we got first,
		*then* switching for whether it is acceptable for our current phase... is by
		far the shorter volume of code to write.
	*/
	phase := d.current
	switch tokenSlot.Type {
	case TMapOpen:
		switch phase {
		case phase_mapDefExpectValue, phase_mapIndefExpectValue:
			d.current -= 1
			fallthrough
		case phase_anyExpectValue, phase_arrDefExpectValueOrEnd, phase_arrIndefExpectValueOrEnd:
			if tokenSlot.Tagged {
				d.emitMajorPlusLen(cborMajorTag, uint64(tokenSlot.Tag))
			}
			if tokenSlot.Length >= 0 {
				d.pushPhase(phase_mapDefExpectKeyOrEnd)
				d.emitMajorPlusLen(cborMajorMap, uint64(tokenSlot.Length))
			} else {
				d.pushPhase(phase_mapIndefExpectKeyOrEnd)
				d.w.writen1(cborSigilIndefiniteMap)
			}
			return false, d.w.checkErr()
		case phase_mapDefExpectKeyOrEnd, phase_mapIndefExpectKeyOrEnd:
			return true, &ErrInvalidTokenStream{Got: *tokenSlot, Acceptable: tokenTypesForKey}
		default:
			panic("unreachable phase")
		}
	case TMapClose:
		switch phase {
		case phase_mapDefExpectKeyOrEnd:
			return d.popPhase(), nil
		case phase_mapIndefExpectKeyOrEnd:
			d.w.writen1(cborSigilBreak)
			return d.popPhase(), d.w.checkErr()
		case phase_anyExpectValue, phase_mapDefExpectValue, phase_mapIndefExpectValue, phase_arrDefExpectValueOrEnd, phase_arrIndefExpectValueOrEnd:
			return true, &ErrInvalidTokenStream{Got: *tokenSlot, Acceptable: tokenTypesForValue}
		default:
			panic("unreachable phase")
		}
	case TArrOpen:
		switch phase {
		case phase_mapDefExpectValue, phase_mapIndefExpectValue:
			d.current -= 1
			fallthrough
		case phase_anyExpectValue, phase_arrDefExpectValueOrEnd, phase_arrIndefExpectValueOrEnd:
			if tokenSlot.Tagged {
				d.emitMajorPlusLen(cborMajorTag, uint64(tokenSlot.Tag))
			}
			if tokenSlot.Length >= 0 {
				d.pushPhase(phase_arrDefExpectValueOrEnd)
				d.emitMajorPlusLen(cborMajorArray, uint64(tokenSlot.Length))
			} else {
				d.pushPhase(phase_arrIndefExpectValueOrEnd)
				d.w.writen1(cborSigilIndefiniteArray)
			}
			return false, d.w.checkErr()
		case phase_mapDefExpectKeyOrEnd, phase_mapIndefExpectKeyOrEnd:
			return true, &ErrInvalidTokenStream{Got: *tokenSlot, Acceptable: tokenTypesForKey}
		default:
			panic("unreachable phase")
		}
	case TArrClose:
		switch phase {
		case phase_arrDefExpectValueOrEnd:
			return d.popPhase(), nil
		case phase_arrIndefExpectValueOrEnd:
			d.w.writen1(cborSigilBreak)
			return d.popPhase(), d.w.checkErr()
		case phase_anyExpectValue, phase_mapDefExpectValue, phase_mapIndefExpectValue:
			return true, &ErrInvalidTokenStream{Got: *tokenSlot, Acceptable: tokenTypesForValue}
		case phase_mapDefExpectKeyOrEnd, phase_mapIndefExpectKeyOrEnd:
			return true, &ErrInvalidTokenStream{Got: *tokenSlot, Acceptable: tokenTypesForKey}
		default:
			panic("unreachable phase")
		}
	case TNull: // terminal value; not accepted as map key.
		switch phase {
		case phase_mapDefExpectValue, phase_mapIndefExpectValue:
			d.current -= 1
			fallthrough
		case phase_anyExpectValue, phase_arrDefExpectValueOrEnd, phase_arrIndefExpectValueOrEnd:
			if tokenSlot.Tagged {
				d.emitMajorPlusLen(cborMajorTag, uint64(tokenSlot.Tag))
			}
			d.w.writen1(cborSigilNil)
			return phase == phase_anyExpectValue, d.w.checkErr()
		case phase_mapDefExpectKeyOrEnd, phase_mapIndefExpectKeyOrEnd:
			return true, &ErrInvalidTokenStream{Got: *tokenSlot, Acceptable: tokenTypesForKey}
		default:
			panic("unreachable phase")
		}
	case TString: // terminal value; YES, accepted as map key.
		switch phase {
		case phase_mapDefExpectValue, phase_mapIndefExpectValue:
			d.current -= 1
			fallthrough
		case phase_anyExpectValue, phase_arrDefExpectValueOrEnd, phase_arrIndefExpectValueOrEnd:
			goto emitStr
		case phase_mapDefExpectKeyOrEnd, phase_mapIndefExpectKeyOrEnd:
			d.current += 1
			goto emitStr
		default:
			panic("unreachable phase")
		}
	emitStr:
		{
			if tokenSlot.Tagged {
				d.emitMajorPlusLen(cborMajorTag, uint64(tokenSlot.Tag))
			}
			d.encodeString(tokenSlot.Str)
			return phase == phase_anyExpectValue, d.w.checkErr()
		}
	case TBytes: // terminal value; not accepted as map key.
		switch phase {
		case phase_mapDefExpectValue, phase_mapIndefExpectValue:
			d.current -= 1
			fallthrough
		case phase_anyExpectValue, phase_arrDefExpectValueOrEnd, phase_arrIndefExpectValueOrEnd:
			if tokenSlot.Tagged {
				d.emitMajorPlusLen(cborMajorTag, uint64(tokenSlot.Tag))
			}
			d.encodeBytes(tokenSlot.Bytes)
			return phase == phase_anyExpectValue, d.w.checkErr()
		case phase_mapDefExpectKeyOrEnd, phase_mapIndefExpectKeyOrEnd:
			return true, &ErrInvalidTokenStream{Got: *tokenSlot, Acceptable: tokenTypesForKey}
		default:
			panic("unreachable phase")
		}
	case TBool: // terminal value; not accepted as map key.
		switch phase {
		case phase_mapDefExpectValue, phase_mapIndefExpectValue:
			d.current -= 1
			fallthrough
		case phase_anyExpectValue, phase_arrDefExpectValueOrEnd, phase_arrIndefExpectValueOrEnd:
			if tokenSlot.Tagged {
				d.emitMajorPlusLen(cborMajorTag, uint64(tokenSlot.Tag))
			}
			d.encodeBool(tokenSlot.Bool)
			return phase == phase_anyExpectValue, d.w.checkErr()
		case phase_mapDefExpectKeyOrEnd, phase_mapIndefExpectKeyOrEnd:
			return true, &ErrInvalidTokenStream{Got: *tokenSlot, Acceptable: tokenTypesForKey}
		default:
			panic("unreachable phase")
		}
	case TInt: // terminal value; YES, accepted as map key.
		switch phase {
		case phase_mapDefExpectValue, phase_mapIndefExpectValue:
			d.current -= 1
			fallthrough
		case phase_anyExpectValue, phase_arrDefExpectValueOrEnd, phase_arrIndefExpectValueOrEnd:
			goto emitInt
		case phase_mapDefExpectKeyOrEnd, phase_mapIndefExpectKeyOrEnd:
			d.current += 1
			goto emitInt
		default:
			panic("unreachable phase")
		}
	emitInt:
		{
			if tokenSlot.Tagged {
				d.emitMajorPlusLen(cborMajorTag, uint64(tokenSlot.Tag))
			}
			d.encodeInt64(tokenSlot.Int)
			return phase == phase_anyExpectValue, d.w.checkErr()
		}
	case TUint: // terminal value; YES, accepted as map key.
		switch phase {
		case phase_mapDefExpectValue, phase_mapIndefExpectValue:
			d.current -= 1
			fallthrough
		case phase_anyExpectValue, phase_arrDefExpectValueOrEnd, phase_arrIndefExpectValueOrEnd:
			goto emitUint
		case phase_mapDefExpectKeyOrEnd, phase_mapIndefExpectKeyOrEnd:
			d.current += 1
			goto emitUint
		default:
			panic("unreachable phase")
		}
	emitUint:
		{
			if tokenSlot.Tagged {
				d.emitMajorPlusLen(cborMajorTag, uint64(tokenSlot.Tag))
			}
			d.encodeUint64(tokenSlot.Uint)
			return phase == phase_anyExpectValue, d.w.checkErr()
		}
	case TFloat64: // terminal value; not accepted as map key.
		switch phase {
		case phase_mapDefExpectValue, phase_mapIndefExpectValue:
			d.current -= 1
			fallthrough
		case phase_anyExpectValue, phase_arrDefExpectValueOrEnd, phase_arrIndefExpectValueOrEnd:
			if tokenSlot.Tagged {
				d.emitMajorPlusLen(cborMajorTag, uint64(tokenSlot.Tag))
			}
			d.encodeFloat64(tokenSlot.Float64)
			return phase == phase_anyExpectValue, d.w.checkErr()
		case phase_mapDefExpectKeyOrEnd, phase_mapIndefExpectKeyOrEnd:
			return true, &ErrInvalidTokenStream{Got: *tokenSlot, Acceptable: tokenTypesForKey}
		default:
			panic("unreachable phase")
		}
	default:
		panic("unhandled token type")
	}
}
