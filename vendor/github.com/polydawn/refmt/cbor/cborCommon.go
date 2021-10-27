package cbor

// "Major types" enum, as per https://tools.ietf.org/html/rfc7049#section-2.1 .
//
// These numbers are the bottom of the range for that major type when encoded;
// that is, ints can be between `cborMajorUint` (inclusive) and `cborMajorNegInt` (exclusive).
// Zero out the 0x1f bitrange of a byte to see which major type it is (those bits are
// used for packing either length info or other specific enumerated meanings).
const (
	cborMajorUint   byte = 0x00
	cborMajorNegInt      = 0x20
	cborMajorBytes       = 0x40
	cborMajorString      = 0x60
	cborMajorArray       = 0x80
	cborMajorMap         = 0xa0
	cborMajorTag         = 0xc0
	cborMajorSimple      = 0xe0 // Floating point, "simple" types like bool, etc, are above.
)

// Enumeration of some values with single fixed-byte representations.
// All of these are in the "simple" space.
// See https://tools.ietf.org/html/rfc7049#section-2.3 for tables.
// The prefix indicating a float is also packed into the simple space.
const (
	cborSigilFalse     byte = 0xf4
	cborSigilTrue           = 0xf5
	cborSigilNil            = 0xf6
	cborSigilUndefined      = 0xf7
	cborSigilFloat16        = 0xf9
	cborSigilFloat32        = 0xfA
	cborSigilFloat64        = 0xfB
)

// The highest value in the range for bytes, text, arrays, and maps all indicate
// an "indefinite length" / "streaming" entry coming up.  These have a different parse path.
// The special 'break' value from the "simple" space (all bits on)
// indicates termination of stream for all four kinds major types in this mode.
const (
	cborSigilIndefiniteBytes  byte = 0x5f
	cborSigilIndefiniteString      = 0x7f
	cborSigilIndefiniteArray       = 0x9f
	cborSigilIndefiniteMap         = 0xbf
	cborSigilBreak                 = 0xff
)
