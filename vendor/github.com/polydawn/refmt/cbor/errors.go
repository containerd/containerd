package cbor

import (
	"fmt"

	. "github.com/polydawn/refmt/tok"
)

// Error raised by Encoder when invalid tokens or invalid ordering, e.g. a MapClose with no matching open.
// Should never be seen by the user in practice unless generating their own token streams.
type ErrInvalidTokenStream struct {
	Got        Token
	Acceptable []TokenType
}

func (e *ErrInvalidTokenStream) Error() string {
	return fmt.Sprintf("ErrInvalidTokenStream: unexpected %v, expected %v", e.Got, e.Acceptable)
	// More comprehensible strings might include "start of value", "start of key or end of map", "start of value or end of array".
}

var tokenTypesForKey = []TokenType{TString, TInt, TUint}
var tokenTypesForValue = []TokenType{TMapOpen, TArrOpen, TNull, TString, TBytes, TInt, TUint, TFloat64}
