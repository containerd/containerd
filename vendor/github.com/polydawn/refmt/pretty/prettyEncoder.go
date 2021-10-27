package pretty

import (
	"encoding/hex"
	"fmt"
	"io"
	"strconv"

	. "github.com/polydawn/refmt/tok"
)

func NewEncoder(wr io.Writer) *Encoder {
	return &Encoder{
		wr:    wr,
		stack: make([]phase, 0, 10),
	}
}

func (d *Encoder) Reset() {
	d.stack = d.stack[0:0]
	d.current = phase_anyExpectValue
}

/*
	A pretty.Encoder is a TokenSink that emits pretty-printed stuff.

	The default behavior is color coded with ANSI escape sequences, so it's
	snazzy looking on your terminal.
*/
type Encoder struct {
	wr io.Writer

	// Stack, tracking how many array and map opens are outstanding.
	// (Values are only 'phase_mapExpectKeyOrEnd' and 'phase_arrExpectValueOrEnd'.)
	stack   []phase
	current phase // shortcut to value at end of stack

	// Spare memory, for use in operations on leaf nodes (e.g. temp space for an int serialization).
	scratch [64]byte
}

type phase int

const (
	phase_anyExpectValue phase = iota
	phase_mapExpectKeyOrEnd
	phase_mapExpectValue
	phase_arrExpectValueOrEnd
)

func (d *Encoder) Step(tok *Token) (done bool, err error) {
	switch d.current {
	case phase_anyExpectValue:
		switch tok.Type {
		case TMapOpen:
			d.pushPhase(phase_mapExpectKeyOrEnd)
			d.emitMapOpen(tok)
			return false, nil
		case TArrOpen:
			d.pushPhase(phase_arrExpectValueOrEnd)
			d.emitArrOpen(tok)
			return false, nil
		case TMapClose:
			return true, fmt.Errorf("unexpected mapClose; expected start of value")
		case TArrClose:
			return true, fmt.Errorf("unexpected arrClose; expected start of value")
		default:
			d.emitValue(tok)
			d.wr.Write(wordBreak)
			return true, nil
		}
	case phase_mapExpectKeyOrEnd:
		switch tok.Type {
		case TMapOpen:
			return true, fmt.Errorf("unexpected mapOpen; expected start of key or end of map")
		case TArrOpen:
			return true, fmt.Errorf("unexpected arrOpen; expected start of key or end of map")
		case TMapClose:
			d.emitMapClose(tok)
			return d.popPhase()
		case TArrClose:
			return true, fmt.Errorf("unexpected arrClose; expected start of key or end of map")
		default:
			switch tok.Type {
			case TString, TInt, TUint:
				d.wr.Write(indentWord(len(d.stack)))
				d.emitValue(tok)
				d.wr.Write(wordColon)
				d.current = phase_mapExpectValue
				return false, nil
			default:
				return true, fmt.Errorf("unexpected token of type %T; expected map key", *tok)
			}
		}
	case phase_mapExpectValue:
		switch tok.Type {
		case TMapOpen:
			d.pushPhase(phase_mapExpectKeyOrEnd)
			d.emitMapOpen(tok)
			return false, nil
		case TArrOpen:
			d.pushPhase(phase_arrExpectValueOrEnd)
			d.emitArrOpen(tok)
			return false, nil
		case TMapClose:
			return true, fmt.Errorf("unexpected mapClose; expected start of value")
		case TArrClose:
			return true, fmt.Errorf("unexpected arrClose; expected start of value")
		default:
			d.current = phase_mapExpectKeyOrEnd
			d.emitValue(tok)
			d.wr.Write(wordBreak)
			return false, nil
		}
	case phase_arrExpectValueOrEnd:
		switch tok.Type {
		case TMapOpen:
			d.pushPhase(phase_mapExpectKeyOrEnd)
			d.emitMapOpen(tok)
			return false, nil
		case TArrOpen:
			d.pushPhase(phase_arrExpectValueOrEnd)
			d.emitArrOpen(tok)
			return false, nil
		case TMapClose:
			return true, fmt.Errorf("unexpected mapClose; expected start of value or end of array")
		case TArrClose:
			d.emitArrClose(tok)
			return d.popPhase()
		default:
			d.wr.Write(indentWord(len(d.stack)))
			d.emitValue(tok)
			d.wr.Write(wordBreak)
			return false, nil
		}
	default:
		panic("Unreachable")
	}
}

func (d *Encoder) pushPhase(p phase) {
	d.current = p
	d.stack = append(d.stack, d.current)
}

// Pop a phase from the stack; return 'true' if stack now empty.
func (d *Encoder) popPhase() (bool, error) {
	n := len(d.stack) - 1
	if n == 0 {
		return true, nil
	}
	if n < 0 { // the state machines are supposed to have already errored better
		panic("prettyEncoder stack overpopped")
	}
	d.current = d.stack[n-1]
	d.stack = d.stack[0:n]
	return false, nil
}

func (d *Encoder) emitMapOpen(tok *Token) {
	if tok.Tagged {
		d.wr.Write(wordTag)
		d.wr.Write([]byte(strconv.Itoa(tok.Tag)))
		d.wr.Write(wordTagClose)
	}
	d.wr.Write(wordMapOpenPt1)
	if tok.Length < 0 {
		d.wr.Write(wordUnknownLen)
	} else {
		d.wr.Write([]byte(strconv.Itoa(tok.Length)))
	}
	d.wr.Write(wordMapOpenPt2)
	d.wr.Write(wordBreak)
}

func (d *Encoder) emitMapClose(tok *Token) {
	d.wr.Write(indentWord(len(d.stack) - 1))
	d.wr.Write(wordMapClose)
	d.wr.Write(wordBreak)
}

func (d *Encoder) emitArrOpen(tok *Token) {
	if tok.Tagged {
		d.wr.Write(wordTag)
		d.wr.Write([]byte(strconv.Itoa(tok.Tag)))
		d.wr.Write(wordTagClose)
	}
	d.wr.Write(wordArrOpenPt1)
	if tok.Length < 0 {
		d.wr.Write(wordUnknownLen)
	} else {
		d.wr.Write([]byte(strconv.Itoa(tok.Length)))
	}
	d.wr.Write(wordArrOpenPt2)
	d.wr.Write(wordBreak)
}

func (d *Encoder) emitArrClose(tok *Token) {
	d.wr.Write(indentWord(len(d.stack) - 1))
	d.wr.Write(wordArrClose)
	d.wr.Write(wordBreak)
}

func (d *Encoder) emitValue(tok *Token) {
	if tok.Tagged {
		d.wr.Write(wordTag)
		d.wr.Write([]byte(strconv.Itoa(tok.Tag)))
		d.wr.Write(wordTagClose)
	}
	switch tok.Type {
	case TNull:
		d.wr.Write(wordNull)
	case TString:
		d.emitString(tok.Str)
	case TBytes:
		dst := make([]byte, hex.EncodedLen(len(tok.Bytes)))
		hex.Encode(dst, tok.Bytes)
		d.wr.Write(dst)
	case TBool:
		switch tok.Bool {
		case true:
			d.wr.Write(wordTrue)
		case false:
			d.wr.Write(wordFalse)
		}
	case TInt:
		b := strconv.AppendInt(d.scratch[:0], tok.Int, 10)
		d.wr.Write(b)
	case TUint:
		b := strconv.AppendUint(d.scratch[:0], tok.Uint, 10)
		d.wr.Write(b)
	case TFloat64:
		b := strconv.AppendFloat(d.scratch[:0], tok.Float64, 'f', 6, 64)
		d.wr.Write(b)
	default:
		panic(fmt.Errorf("TODO finish more pretty.Encoder primitives support: unhandled token %s", tok))
	}
}

func (d *Encoder) writeByte(b byte) {
	d.scratch[0] = b
	d.wr.Write(d.scratch[0:1])
}
