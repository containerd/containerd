/*
	Package containing Token struct and TokenType info.
	Tokens are the lingua-franca used between all the refmt packages.
	Users typically do not refer to these types.
*/
package tok

import (
	"bytes"
	"fmt"
)

type Token struct {
	// The type of token.  Indicates which of the value fields has meaning,
	// or has a special value to indicate beginnings and endings of maps and arrays.
	Type   TokenType
	Length int // If this is a TMapOpen or TArrOpen, a length may be specified.  Use -1 for unknown.

	Str     string  // Value union.  Only one of these has meaning, depending on the value of 'Type'.
	Bytes   []byte  // Value union.  Only one of these has meaning, depending on the value of 'Type'.
	Bool    bool    // Value union.  Only one of these has meaning, depending on the value of 'Type'.
	Int     int64   // Value union.  Only one of these has meaning, depending on the value of 'Type'.
	Uint    uint64  // Value union.  Only one of these has meaning, depending on the value of 'Type'.
	Float64 float64 // Value union.  Only one of these has meaning, depending on the value of 'Type'.

	Tagged bool // Extension slot for cbor.
	Tag    int  // Extension slot for cbor.  Only applicable if tagged=true.
}

type TokenType byte

const (
	TMapOpen  TokenType = '{'
	TMapClose TokenType = '}'
	TArrOpen  TokenType = '['
	TArrClose TokenType = ']'
	TNull     TokenType = '0'

	TString  TokenType = 's'
	TBytes   TokenType = 'x'
	TBool    TokenType = 'b'
	TInt     TokenType = 'i'
	TUint    TokenType = 'u'
	TFloat64 TokenType = 'f'
)

func (tt TokenType) String() string {
	switch tt {
	case TMapOpen:
		return "map open"
	case TMapClose:
		return "map close"
	case TArrOpen:
		return "array open"
	case TArrClose:
		return "array close"
	case TNull:
		return "null"
	case TString:
		return "string"
	case TBytes:
		return "bytes"
	case TBool:
		return "bool"
	case TInt:
		return "int"
	case TUint:
		return "uint"
	case TFloat64:
		return "float"
	}
	return "invalid"
}

func (tt TokenType) IsValid() bool {
	switch tt {
	case TString, TBytes, TBool, TInt, TUint, TFloat64, TNull:
		return true
	case TMapOpen, TMapClose, TArrOpen, TArrClose:
		return true
	default:
		return false
	}
}

func (tt TokenType) IsValue() bool {
	switch tt {
	case TString, TBytes, TBool, TInt, TUint, TFloat64:
		return true
	default:
		return false
	}
}

func (tt TokenType) IsSpecial() bool {
	switch tt {
	case TMapOpen, TMapClose, TArrOpen, TArrClose, TNull:
		return true
	default:
		return false
	}
}

/*
	Checks if the content of two tokens is the same.
	Tokens are considered the same if their type one of the special
	consts (map/array open/close) and that type and the optional length field are equal;
	or, if type indicates a value, then they are the same if those values are equal.
	In either path, values that are *not* specified as relevant by the Token's Type
	are disregarded in the comparison.

	If the Token.Type is not valid, the result will be false.

	This method is primarily useful for testing.
*/
func IsTokenEqual(t1, t2 Token) bool {
	if t1.Type != t2.Type {
		return false
	}
	switch t1.Type {
	case TMapOpen, TArrOpen:
		return t1.Length == t2.Length
	case TMapClose, TArrClose, TNull:
		return true
	case TString, TBool, TInt, TUint, TFloat64:
		return t1.Value() == t2.Value()
	case TBytes:
		return bytes.Equal(t1.Bytes, t2.Bytes)
	default:
		return false
	}
}

// Returns the value attached to this token, or nil.
// This boxes the value into an `interface{}`, which almost certainly
// incurs a memory allocation via `runtime.convT2E` in the process,
// so this this method should not be used when performance is a concern.
func (t Token) Value() interface{} {
	switch t.Type {
	case TString:
		return t.Str
	case TBytes:
		return t.Bytes
	case TBool:
		return t.Bool
	case TInt:
		return t.Int
	case TUint:
		return t.Uint
	case TFloat64:
		return t.Float64
	default:
		return nil
	}
}

func (t Token) String() string {
	if !t.Tagged {
		return t.StringSansTag()
	}
	return fmt.Sprintf("_%d:%s", t.Tag, t.StringSansTag())
}

func (t Token) StringSansTag() string {
	switch t.Type {
	case TMapOpen:
		if t.Length == -1 {
			return "<{>"
		}
		return fmt.Sprintf("<{:%d>", t.Length)
	case TMapClose:
		return "<}>"
	case TArrOpen:
		if t.Length == -1 {
			return "<[>"
		}
		return fmt.Sprintf("<[:%d>", t.Length)
	case TArrClose:
		return "<]>"
	case TNull:
		return "<0>"
	case TString:
		return fmt.Sprintf("<%c:%q>", t.Type, t.Value())
	}
	if t.Type.IsValue() {
		return fmt.Sprintf("<%c:%v>", t.Type, t.Value())
	}
	return "<INVALID>"
}

func TokStr(x string) Token { return Token{Type: TString, Str: x} } // Util for testing.
func TokInt(x int64) Token  { return Token{Type: TInt, Int: x} }    // Util for testing.
