package json

// License note: the string and numeric parsers here borrow
// heavily from the golang stdlib json parser scanner.
// That code is originally Copyright 2010 The Go Authors,
// and is governed by a BSD-style license.

import (
	"fmt"
	"io"
	"math"
	"strconv"
	"unicode/utf8"
)

var hex = "0123456789abcdef"

func (d *Encoder) emitString(s string) {
	d.writeByte('"')
	start := 0
	for i := 0; i < len(s); {
		if b := s[i]; b < utf8.RuneSelf {
			if 0x20 <= b && b != '\\' && b != '"' {
				i++
				continue
			}
			if start < i {
				d.wr.Write([]byte(s[start:i]))
			}
			switch b {
			case '\\', '"':
				d.writeByte('\\')
				d.writeByte(b)
			case '\n':
				d.writeByte('\\')
				d.writeByte('n')
			case '\r':
				d.writeByte('\\')
				d.writeByte('r')
			case '\t':
				d.writeByte('\\')
				d.writeByte('t')
			default:
				// This encodes bytes < 0x20 except for \t, \n and \r.
				d.wr.Write([]byte(`\u00`))
				d.writeByte(hex[b>>4])
				d.writeByte(hex[b&0xF])
			}
			i++
			start = i
			continue
		}
		c, size := utf8.DecodeRuneInString(s[i:])
		if c == utf8.RuneError && size == 1 {
			if start < i {
				d.wr.Write([]byte(s[start:i]))
			}
			d.wr.Write([]byte(`\ufffd`))
			i += size
			start = i
			continue
		}
		// U+2028 is LINE SEPARATOR.
		// U+2029 is PARAGRAPH SEPARATOR.
		// They are both technically valid characters in JSON strings,
		// but don't work in JSONP, which has to be evaluated as JavaScript,
		// and can lead to security holes there. It is valid JSON to
		// escape them, so we do so unconditionally.
		// See http://timelessrepo.com/json-isnt-a-javascript-subset for discussion.
		if c == '\u2028' || c == '\u2029' {
			if start < i {
				d.wr.Write([]byte(s[start:i]))
			}
			d.wr.Write([]byte(`\u202`))
			d.writeByte(hex[c&0xF])
			i += size
			start = i
			continue
		}
		i += size
	}
	if start < len(s) {
		io.WriteString(d.wr, s[start:])
	}
	d.writeByte('"')
}

func (d *Encoder) emitFloat(f float64) error {
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return fmt.Errorf("unsupported value: %s", strconv.FormatFloat(f, 'g', -1, int(64)))
	}

	// Convert as if by ES6 number to string conversion.
	// This matches most other JSON generators.
	// See golang.org/issue/6384 and golang.org/issue/14135.
	// Like fmt %g, but the exponent cutoffs are different
	// and exponents themselves are not padded to two digits.
	b := d.scratch[:0]
	abs := math.Abs(f)
	fmt := byte('f')
	if abs != 0 {
		if abs < 1e-6 || abs >= 1e21 {
			fmt = 'e'
		}
	}
	b = strconv.AppendFloat(b, f, fmt, -1, int(64))
	if fmt == 'e' {
		// clean up e-09 to e-9
		n := len(b)
		if n >= 4 && b[n-4] == 'e' && b[n-3] == '-' && b[n-2] == '0' {
			b[n-2] = b[n-1]
			b = b[:n-1]
		}
	}
	d.wr.Write(b)
	return nil
}
