package json

// License note: the string and numeric parsers here borrow
// heavily from the golang stdlib json parser scanner.
// That code is originally Copyright 2010 The Go Authors,
// and is governed by a BSD-style license.

import (
	"fmt"
	"io"
	"strconv"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/polydawn/refmt/tok"
)

func (d *Decoder) decodeString() (string, error) {
	// First quote has already been eaten.
	// Start tracking the byte slice; real string starts here.
	d.r.Track()
	// Scan until scanner tells us end of string.
	for step := strscan_normal; step != nil; {
		majorByte, err := d.r.Readn1()
		if err != nil {
			return "", err
		}
		step, err = step(majorByte)
		if err != nil {
			return "", err
		}
	}
	// Unread one.  The scan loop consumed the trailing quote already,
	// which we don't want to pass onto the parser.
	d.r.Unreadn1()
	// Parse!
	s, ok := parseString(d.r.StopTrack())
	if !ok {
		//return string(s), fmt.Errorf("string parse misc fail")
	}
	// Swallow the trailing quote again.
	d.r.Readn1()
	return string(s), nil
}

// Scan steps are looped over the stream to find how long the string is.
// A nil step func is returned to indicate the string is done.
// Actually parsing the string is done by 'parseString()'.
type strscanStep func(c byte) (strscanStep, error)

// The default string scanning step state.  Starts here.
func strscan_normal(c byte) (strscanStep, error) {
	if c == '"' { // done!
		return nil, nil
	}
	if c == '\\' {
		return strscan_esc, nil
	}
	if c < 0x20 { // Unprintable bytes are invalid in a json string.
		return nil, fmt.Errorf("invalid unprintable byte in string literal: 0x%x", c)
	}
	return strscan_normal, nil
}

// "esc" is the state after reading `"\` during a quoted string.
func strscan_esc(c byte) (strscanStep, error) {
	switch c {
	case 'b', 'f', 'n', 'r', 't', '\\', '/', '"':
		return strscan_normal, nil
	case 'u':
		return strscan_escU, nil
	}
	return nil, fmt.Errorf("invalid byte in string escape sequence: 0x%x", c)
}

// "escU" is the state after reading `"\u` during a quoted string.
func strscan_escU(c byte) (strscanStep, error) {
	if '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F' {
		return strscan_escU1, nil
	}
	return nil, fmt.Errorf("invalid byte in \\u hexadecimal character escape: 0x%x", c)
}

// "escU1" is the state after reading `"\u1` during a quoted string.
func strscan_escU1(c byte) (strscanStep, error) {
	if '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F' {
		return strscan_escU12, nil
	}
	return nil, fmt.Errorf("invalid byte in \\u hexadecimal character escape: 0x%x", c)
}

// "escU12" is the state after reading `"\u12` during a quoted string.
func strscan_escU12(c byte) (strscanStep, error) {
	if '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F' {
		return strscan_escU123, nil
	}
	return nil, fmt.Errorf("invalid byte in \\u hexadecimal character escape: 0x%x", c)
}

// "escU123" is the state after reading `"\u123` during a quoted string.
func strscan_escU123(c byte) (strscanStep, error) {
	if '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F' {
		return strscan_normal, nil
	}
	return nil, fmt.Errorf("invalid byte in \\u hexadecimal character escape: 0x%x", c)
}

// Convert a json string byte sequence that is a complete string (quotes from
// the outside dropped) bytes ready to be flipped into a go string.
func parseString(s []byte) (t []byte, ok bool) {
	// Check for unusual characters. If there are none,
	// then no unquoting is needed, so return a slice of the
	// original bytes.
	r := 0
	for r < len(s) {
		c := s[r]
		if c == '\\' || c == '"' || c < ' ' {
			break
		}
		if c < utf8.RuneSelf {
			r++
			continue
		}
		rr, size := utf8.DecodeRune(s[r:])
		if rr == utf8.RuneError && size == 1 {
			break
		}
		r += size
	}
	if r == len(s) {
		return s, true
	}

	b := make([]byte, len(s)+2*utf8.UTFMax)
	w := copy(b, s[0:r])
	for r < len(s) {
		// Out of room?  Can only happen if s is full of
		// malformed UTF-8 and we're replacing each
		// byte with RuneError.
		if w >= len(b)-2*utf8.UTFMax {
			nb := make([]byte, (len(b)+utf8.UTFMax)*2)
			copy(nb, b[0:w])
			b = nb
		}
		switch c := s[r]; {
		case c == '\\':
			r++
			if r >= len(s) {
				return
			}
			switch s[r] {
			default:
				return
			case '"', '\\', '/', '\'':
				b[w] = s[r]
				r++
				w++
			case 'b':
				b[w] = '\b'
				r++
				w++
			case 'f':
				b[w] = '\f'
				r++
				w++
			case 'n':
				b[w] = '\n'
				r++
				w++
			case 'r':
				b[w] = '\r'
				r++
				w++
			case 't':
				b[w] = '\t'
				r++
				w++
			case 'u':
				r--
				rr := getu4(s[r:])
				if rr < 0 {
					return
				}
				r += 6
				if utf16.IsSurrogate(rr) {
					rr1 := getu4(s[r:])
					if dec := utf16.DecodeRune(rr, rr1); dec != unicode.ReplacementChar {
						// A valid pair; consume.
						r += 6
						w += utf8.EncodeRune(b[w:], dec)
						break
					}
					// Invalid surrogate; fall back to replacement rune.
					rr = unicode.ReplacementChar
				}
				w += utf8.EncodeRune(b[w:], rr)
			}

		// Quote, control characters are invalid.
		case c == '"', c < ' ':
			return

		// ASCII
		case c < utf8.RuneSelf:
			b[w] = c
			r++
			w++

		// Coerce to well-formed UTF-8.
		default:
			rr, size := utf8.DecodeRune(s[r:])
			r += size
			w += utf8.EncodeRune(b[w:], rr)
		}
	}
	return b[0:w], true
}

// getu4 decodes \uXXXX from the beginning of s, returning the hex value,
// or it returns -1.
func getu4(s []byte) rune {
	if len(s) < 6 || s[0] != '\\' || s[1] != 'u' {
		return -1
	}
	r, err := strconv.ParseUint(string(s[2:6]), 16, 64)
	if err != nil {
		return -1
	}
	return rune(r)
}

// Returns *either* an int or a float -- json is ambigous.
// An int is preferred if possible.
func (d *Decoder) decodeNumber(majorByte byte) (tok.TokenType, int64, float64, error) {
	// First byte has already been eaten.
	// Easiest to unread1, so we can use track, then swallow it again.
	d.r.Unreadn1()
	d.r.Track()
	d.r.Readn1()
	// Scan until scanner tells us end of numeric.
	// Pick the first scanner stepfunc based on the leading byte.
	var step numscanStep
	switch majorByte {
	case '-':
		step = numscan_neg
	case '0':
		step = numscan_0
	case '1', '2', '3', '4', '5', '6', '7', '8', '9':
		step = numscan_1
	default:
		panic("unreachable")
	}
	for {
		b, err := d.r.Readn1()
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, 0, 0, err
		}
		step, err = step(b)
		if step == nil {
			// Unread one.  The scan loop consumed one char beyond the end
			// (this is necessary in json!),
			// which the next part of the decoder will need elsewhere.
			d.r.Unreadn1()
			break
		}
		if err != nil {
			return 0, 0, 0, err
		}
	}
	// Parse!
	// *This is not a fast parse*.
	// Try int first; if it fails for range reasons, halt; otherwise,
	// then try float; if that fails return the float error.
	s := string(d.r.StopTrack())
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return tok.TInt, i, 0, nil
	} else if err.(*strconv.NumError).Err == strconv.ErrRange {
		return tok.TInt, i, 0, err
	}
	f, err := strconv.ParseFloat(s, 64)
	return tok.TFloat64, 0, f, err
}

// Scan steps are looped over the stream to find how long the number is.
// A nil step func is returned to indicate the string is done.
// Actually parsing the string is done by 'parseString()'.
type numscanStep func(c byte) (numscanStep, error)

// numscan_neg is the state after reading `-` during a number.
func numscan_neg(c byte) (numscanStep, error) {
	if c == '0' {
		return numscan_0, nil
	}
	if '1' <= c && c <= '9' {
		return numscan_1, nil
	}
	return nil, fmt.Errorf("invalid byte in numeric literal: 0x%x", c)
}

// numscan_1 is the state after reading a non-zero integer during a number,
// such as after reading `1` or `100` but not `0`.
func numscan_1(c byte) (numscanStep, error) {
	if '0' <= c && c <= '9' {
		return numscan_1, nil
	}
	return numscan_0(c)
}

// numscan_0 is the state after reading `0` during a number.
func numscan_0(c byte) (numscanStep, error) {
	if c == '.' {
		return numscan_dot, nil
	}
	if c == 'e' || c == 'E' {
		return numscan_e, nil
	}
	return nil, nil
}

// numscan_dot is the state after reading the integer and decimal point in a number,
// such as after reading `1.`.
func numscan_dot(c byte) (numscanStep, error) {
	if '0' <= c && c <= '9' {
		return numscan_dot0, nil
	}
	return nil, fmt.Errorf("invalid byte after decimal in numeric literal: 0x%x", c)
}

// numscan_dot0 is the state after reading the integer, decimal point, and subsequent
// digits of a number, such as after reading `3.14`.
func numscan_dot0(c byte) (numscanStep, error) {
	if '0' <= c && c <= '9' {
		return numscan_dot0, nil
	}
	if c == 'e' || c == 'E' {
		return numscan_e, nil
	}
	return nil, nil
}

// numscan_e is the state after reading the mantissa and e in a number,
// such as after reading `314e` or `0.314e`.
func numscan_e(c byte) (numscanStep, error) {
	if c == '+' || c == '-' {
		return numscan_eSign, nil
	}
	return numscan_eSign(c)
}

// numscan_eSign is the state after reading the mantissa, e, and sign in a number,
// such as after reading `314e-` or `0.314e+`.
func numscan_eSign(c byte) (numscanStep, error) {
	if '0' <= c && c <= '9' {
		return numscan_e0, nil
	}
	return nil, fmt.Errorf("invalid byte in exponent of numeric literal: 0x%x", c)
}

// numscan_e0 is the state after reading the mantissa, e, optional sign,
// and at least one digit of the exponent in a number,
// such as after reading `314e-2` or `0.314e+1` or `3.14e0`.
func numscan_e0(c byte) (numscanStep, error) {
	if '0' <= c && c <= '9' {
		return numscan_e0, nil
	}
	return nil, nil
}
