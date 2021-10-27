// Package multihash is the Go implementation of
// https://github.com/multiformats/multihash, or self-describing
// hashes.
package multihash

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math"

	b58 "github.com/mr-tron/base58/base58"
	"github.com/multiformats/go-varint"
)

// errors
var (
	ErrUnknownCode      = errors.New("unknown multihash code")
	ErrTooShort         = errors.New("multihash too short. must be >= 2 bytes")
	ErrTooLong          = errors.New("multihash too long. must be < 129 bytes")
	ErrLenNotSupported  = errors.New("multihash does not yet support digests longer than 127 bytes")
	ErrInvalidMultihash = errors.New("input isn't valid multihash")

	ErrVarintBufferShort = errors.New("uvarint: buffer too small")
	ErrVarintTooLong     = errors.New("uvarint: varint too big (max 64bit)")
)

// ErrInconsistentLen is returned when a decoded multihash has an inconsistent length
type ErrInconsistentLen struct {
	dm *DecodedMultihash
}

func (e ErrInconsistentLen) Error() string {
	return fmt.Sprintf("multihash length inconsistent: expected %d, got %d", e.dm.Length, len(e.dm.Digest))
}

// constants
const (
	IDENTITY = 0x00
	// Deprecated: use IDENTITY
	ID         = IDENTITY
	SHA1       = 0x11
	SHA2_256   = 0x12
	SHA2_512   = 0x13
	SHA3_224   = 0x17
	SHA3_256   = 0x16
	SHA3_384   = 0x15
	SHA3_512   = 0x14
	SHA3       = SHA3_512
	KECCAK_224 = 0x1A
	KECCAK_256 = 0x1B
	KECCAK_384 = 0x1C
	KECCAK_512 = 0x1D

	SHAKE_128 = 0x18
	SHAKE_256 = 0x19

	BLAKE2B_MIN = 0xb201
	BLAKE2B_MAX = 0xb240
	BLAKE2S_MIN = 0xb241
	BLAKE2S_MAX = 0xb260

	MD5 = 0xd5

	DBL_SHA2_256 = 0x56

	MURMUR3_128 = 0x22
	// Deprecated: use MURMUR3_128
	MURMUR3 = MURMUR3_128

	SHA2_256_TRUNC254_PADDED  = 0x1012
	X11                       = 0x1100
	POSEIDON_BLS12_381_A1_FC1 = 0xb401
)

func init() {
	// Add blake2b (64 codes)
	for c := uint64(BLAKE2B_MIN); c <= BLAKE2B_MAX; c++ {
		n := c - BLAKE2B_MIN + 1
		name := fmt.Sprintf("blake2b-%d", n*8)
		Names[name] = c
		Codes[c] = name
	}

	// Add blake2s (32 codes)
	for c := uint64(BLAKE2S_MIN); c <= BLAKE2S_MAX; c++ {
		n := c - BLAKE2S_MIN + 1
		name := fmt.Sprintf("blake2s-%d", n*8)
		Names[name] = c
		Codes[c] = name
	}
}

// Names maps the name of a hash to the code
var Names = map[string]uint64{
	"identity":                  IDENTITY,
	"sha1":                      SHA1,
	"sha2-256":                  SHA2_256,
	"sha2-512":                  SHA2_512,
	"sha3":                      SHA3_512,
	"sha3-224":                  SHA3_224,
	"sha3-256":                  SHA3_256,
	"sha3-384":                  SHA3_384,
	"sha3-512":                  SHA3_512,
	"dbl-sha2-256":              DBL_SHA2_256,
	"murmur3-128":               MURMUR3_128,
	"keccak-224":                KECCAK_224,
	"keccak-256":                KECCAK_256,
	"keccak-384":                KECCAK_384,
	"keccak-512":                KECCAK_512,
	"shake-128":                 SHAKE_128,
	"shake-256":                 SHAKE_256,
	"sha2-256-trunc254-padded":  SHA2_256_TRUNC254_PADDED,
	"x11":                       X11,
	"md5":                       MD5,
	"poseidon-bls12_381-a2-fc1": POSEIDON_BLS12_381_A1_FC1,
}

// Codes maps a hash code to it's name
var Codes = map[uint64]string{
	IDENTITY:                  "identity",
	SHA1:                      "sha1",
	SHA2_256:                  "sha2-256",
	SHA2_512:                  "sha2-512",
	SHA3_224:                  "sha3-224",
	SHA3_256:                  "sha3-256",
	SHA3_384:                  "sha3-384",
	SHA3_512:                  "sha3-512",
	DBL_SHA2_256:              "dbl-sha2-256",
	MURMUR3_128:               "murmur3-128",
	KECCAK_224:                "keccak-224",
	KECCAK_256:                "keccak-256",
	KECCAK_384:                "keccak-384",
	KECCAK_512:                "keccak-512",
	SHAKE_128:                 "shake-128",
	SHAKE_256:                 "shake-256",
	SHA2_256_TRUNC254_PADDED:  "sha2-256-trunc254-padded",
	X11:                       "x11",
	POSEIDON_BLS12_381_A1_FC1: "poseidon-bls12_381-a2-fc1",
	MD5:                       "md5",
}

func uvarint(buf []byte) (uint64, []byte, error) {
	n, c, err := varint.FromUvarint(buf)
	if err != nil {
		return n, buf, err
	}

	if c == 0 {
		return n, buf, ErrVarintBufferShort
	} else if c < 0 {
		return n, buf[-c:], ErrVarintTooLong
	} else {
		return n, buf[c:], nil
	}
}

// DecodedMultihash represents a parsed multihash and allows
// easy access to the different parts of a multihash.
type DecodedMultihash struct {
	Code   uint64
	Name   string
	Length int    // Length is just int as it is type of len() opearator
	Digest []byte // Digest holds the raw multihash bytes
}

// Multihash is byte slice with the following form:
// <hash function code><digest size><hash function output>.
// See the spec for more information.
type Multihash []byte

// HexString returns the hex-encoded representation of a multihash.
func (m *Multihash) HexString() string {
	return hex.EncodeToString([]byte(*m))
}

// String is an alias to HexString().
func (m *Multihash) String() string {
	return m.HexString()
}

// FromHexString parses a hex-encoded multihash.
func FromHexString(s string) (Multihash, error) {
	b, err := hex.DecodeString(s)
	if err != nil {
		return Multihash{}, err
	}

	return Cast(b)
}

// B58String returns the B58-encoded representation of a multihash.
func (m Multihash) B58String() string {
	return b58.Encode([]byte(m))
}

// FromB58String parses a B58-encoded multihash.
func FromB58String(s string) (m Multihash, err error) {
	b, err := b58.Decode(s)
	if err != nil {
		return Multihash{}, ErrInvalidMultihash
	}

	return Cast(b)
}

// Cast casts a buffer onto a multihash, and returns an error
// if it does not work.
func Cast(buf []byte) (Multihash, error) {
	_, err := Decode(buf)
	if err != nil {
		return Multihash{}, err
	}

	return Multihash(buf), nil
}

// Decode parses multihash bytes into a DecodedMultihash.
func Decode(buf []byte) (*DecodedMultihash, error) {
	rlen, code, hdig, err := readMultihashFromBuf(buf)
	if err != nil {
		return nil, err
	}

	dm := &DecodedMultihash{
		Code:   code,
		Name:   Codes[code],
		Length: len(hdig),
		Digest: hdig,
	}

	if len(buf) != rlen {
		return nil, ErrInconsistentLen{dm}
	}

	return dm, nil
}

// Encode a hash digest along with the specified function code.
// Note: the length is derived from the length of the digest itself.
//
// The error return is legacy; it is always nil.
func Encode(buf []byte, code uint64) ([]byte, error) {
	// FUTURE: this function always causes heap allocs... but when used, this value is almost always going to be appended to another buffer (either as part of CID creation, or etc) -- should this whole function be rethought and alternatives offered?
	newBuf := make([]byte, varint.UvarintSize(code)+varint.UvarintSize(uint64(len(buf)))+len(buf))
	n := varint.PutUvarint(newBuf, code)
	n += varint.PutUvarint(newBuf[n:], uint64(len(buf)))

	copy(newBuf[n:], buf)
	return newBuf, nil
}

// EncodeName is like Encode() but providing a string name
// instead of a numeric code. See Names for allowed values.
func EncodeName(buf []byte, name string) ([]byte, error) {
	return Encode(buf, Names[name])
}

// readMultihashFromBuf reads a multihash from the given buffer, returning the
// individual pieces of the multihash.
// Note: the returned digest is a slice over the passed in data and should be
// copied if the buffer will be reused
func readMultihashFromBuf(buf []byte) (int, uint64, []byte, error) {
	bufl := len(buf)
	if bufl < 2 {
		return 0, 0, nil, ErrTooShort
	}

	var err error
	var code, length uint64

	code, buf, err = uvarint(buf)
	if err != nil {
		return 0, 0, nil, err
	}

	length, buf, err = uvarint(buf)
	if err != nil {
		return 0, 0, nil, err
	}

	if length > math.MaxInt32 {
		return 0, 0, nil, errors.New("digest too long, supporting only <= 2^31-1")
	}
	if int(length) > len(buf) {
		return 0, 0, nil, errors.New("length greater than remaining number of bytes in buffer")
	}

	rlen := (bufl - len(buf)) + int(length)
	return rlen, code, buf[:length], nil
}

// MHFromBytes reads a multihash from the given byte buffer, returning the
// number of bytes read as well as the multihash
func MHFromBytes(buf []byte) (int, Multihash, error) {
	nr, _, _, err := readMultihashFromBuf(buf)
	if err != nil {
		return 0, nil, err
	}

	return nr, Multihash(buf[:nr]), nil
}
