package json

import (
	"fmt"
	"io"

	"github.com/polydawn/refmt/shared"
	. "github.com/polydawn/refmt/tok"
)

type Decoder struct {
	r shared.SlickReader

	stack []decoderStep // When empty, and step returns done, all done.
	step  decoderStep   // Shortcut to end of stack.
	some  bool          // Set to true after first value in any context; use to decide if a comma must precede the next value.
}

func NewDecoder(r io.Reader) (d *Decoder) {
	d = &Decoder{
		r:     shared.NewReader(r),
		stack: make([]decoderStep, 0, 10),
	}
	d.step = d.step_acceptValue
	return
}

func (d *Decoder) Reset() {
	d.stack = d.stack[0:0]
	d.step = d.step_acceptValue
	d.some = false
}

type decoderStep func(tokenSlot *Token) (done bool, err error)

func (d *Decoder) Step(tokenSlot *Token) (done bool, err error) {
	done, err = d.step(tokenSlot)
	// If the step errored: out, entirely.
	if err != nil {
		return true, err
	}
	// If the step wasn't done, return same status.
	if !done {
		return false, nil
	}
	// If it WAS done, and stack empty, we're entirely done.
	nSteps := len(d.stack) - 1
	if nSteps <= 0 {
		return true, nil // that's all folks
	}
	// Pop the stack.  Reset "some" to true.
	d.step = d.stack[nSteps]
	d.stack = d.stack[0:nSteps]
	d.some = true
	return false, nil
}

func (d *Decoder) pushPhase(newPhase decoderStep) {
	d.stack = append(d.stack, d.step)
	d.step = newPhase
	d.some = false
}

func readn1skippingWhitespace(r shared.SlickReader) (majorByte byte, err error) {
	for {
		majorByte, err = r.Readn1()
		switch majorByte {
		case ' ', '\t', '\r', '\n': // continue
		default:
			return
		}
	}
}

// The original step, where any value is accepted, and no terminators for composites are valid.
// ONLY used in the original step; all other steps handle leaf nodes internally.
func (d *Decoder) step_acceptValue(tokenSlot *Token) (done bool, err error) {
	majorByte, err := readn1skippingWhitespace(d.r)
	if err != nil {
		return true, err
	}
	return d.stepHelper_acceptValue(majorByte, tokenSlot)
}

// Step in midst of decoding an array.
func (d *Decoder) step_acceptArrValueOrBreak(tokenSlot *Token) (done bool, err error) {
	majorByte, err := readn1skippingWhitespace(d.r)
	if err != nil {
		return true, err
	}
	if d.some {
		switch majorByte {
		case ']':
			tokenSlot.Type = TArrClose
			return true, nil
		case ',':
			majorByte, err = readn1skippingWhitespace(d.r)
			if err != nil {
				return true, err
			}
			// and now fall through to the next switch
		}
	}
	switch majorByte {
	case ']':
		tokenSlot.Type = TArrClose
		return true, nil
	default:
		_, err := d.stepHelper_acceptValue(majorByte, tokenSlot)
		d.some = true
		return false, err
	}
}

// Step in midst of decoding a map, key expected up next, or end.
func (d *Decoder) step_acceptMapKeyOrBreak(tokenSlot *Token) (done bool, err error) {
	majorByte, err := readn1skippingWhitespace(d.r)
	if err != nil {
		return true, err
	}
	if d.some {
		switch majorByte {
		case '}':
			tokenSlot.Type = TMapClose
			return true, nil
		case ',':
			majorByte, err = readn1skippingWhitespace(d.r)
			if err != nil {
				return true, err
			}
			// and now fall through to the next switch
		}
	}
	switch majorByte {
	case '}':
		tokenSlot.Type = TMapClose
		return true, nil
	default:
		// Consume a string for key.
		_, err := d.stepHelper_acceptValue(majorByte, tokenSlot) // FIXME surely not *any* value?  not composites, at least?
		// Now scan up to consume the colon as well, which is required next.
		majorByte, err = readn1skippingWhitespace(d.r)
		if err != nil {
			return true, err
		}
		if majorByte != ':' {
			return true, fmt.Errorf("expected colon after map key; got 0x%x", majorByte)
		}
		// Next up: expect a value.
		d.step = d.step_acceptMapValue
		d.some = true
		return false, err
	}
}

// Step in midst of decoding a map, value expected up next.
func (d *Decoder) step_acceptMapValue(tokenSlot *Token) (done bool, err error) {
	majorByte, err := readn1skippingWhitespace(d.r)
	if err != nil {
		return true, err
	}
	d.step = d.step_acceptMapKeyOrBreak
	_, err = d.stepHelper_acceptValue(majorByte, tokenSlot)
	return false, err
}

func (d *Decoder) stepHelper_acceptValue(majorByte byte, tokenSlot *Token) (done bool, err error) {
	switch majorByte {
	case '{':
		tokenSlot.Type = TMapOpen
		tokenSlot.Length = -1
		d.pushPhase(d.step_acceptMapKeyOrBreak)
		return false, nil
	case '[':
		tokenSlot.Type = TArrOpen
		tokenSlot.Length = -1
		d.pushPhase(d.step_acceptArrValueOrBreak)
		return false, nil
	case 'n':
		d.r.Readnzc(3) // FIXME must check these equal "ull"!
		tokenSlot.Type = TNull
		return true, nil
	case '"':
		tokenSlot.Type = TString
		tokenSlot.Str, err = d.decodeString()
		return true, err
	case 'f':
		d.r.Readnzc(4) // FIXME must check these equal "alse"!
		tokenSlot.Type = TBool
		tokenSlot.Bool = false
		return true, nil
	case 't':
		d.r.Readnzc(3) // FIXME must check these equal "rue"!
		tokenSlot.Type = TBool
		tokenSlot.Bool = true
		return true, nil
	case '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		// Some kind of numeric... but in json, we *can't tell* if it's float or int.
		// JSON in general doesn't differentiate.  But we usually try to anyway.
		// (If this results in us yielding an int, and an obj.Unmarshaller is filling a float,
		// it's the Unmarshaller responsibility to decide to cast that.)
		tokenSlot.Type, tokenSlot.Int, tokenSlot.Float64, err = d.decodeNumber(majorByte)
		return true, err
	default:
		return true, fmt.Errorf("Invalid byte while expecting start of value: 0x%x", majorByte)
	}
}
