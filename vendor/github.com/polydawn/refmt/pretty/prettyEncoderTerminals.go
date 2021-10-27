package pretty

import (
	"io"
	"unicode/utf8"
)

var hexStr = "0123456789abcdef"

func (d *Encoder) emitString(s string) {
	d.wr.Write(decoValSigil)
	d.writeByte('"')
	d.wr.Write(decoValString)
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
				d.writeByte(hexStr[b>>4])
				d.writeByte(hexStr[b&0xF])
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
			d.writeByte(hexStr[c&0xF])
			i += size
			start = i
			continue
		}
		i += size
	}
	if start < len(s) {
		io.WriteString(d.wr, s[start:])
	}
	d.wr.Write(decoValSigil)
	d.writeByte('"')
	d.wr.Write(decoOff)
}
