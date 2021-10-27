/*

Package base36 provides a reasonably fast implementation of a binary base36 codec.

*/
package base36

// Simplified code based on https://godoc.org/github.com/mr-tron/base58
// which in turn is based on https://github.com/trezor/trezor-crypto/commit/89a7d7797b806fac

import (
	"fmt"
)

const UcAlphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"
const LcAlphabet = "0123456789abcdefghijklmnopqrstuvwxyz"
const maxDigitOrdinal = byte('z')
const maxDigitValueB36 = 35

var revAlphabet [maxDigitOrdinal + 1]byte

func init() {
	for i := range revAlphabet {
		revAlphabet[i] = maxDigitValueB36 + 1
	}
	for i, c := range UcAlphabet {
		revAlphabet[byte(c)] = byte(i)
		if c > '9' {
			revAlphabet[byte(c)+32] = byte(i)
		}
	}
}

// EncodeToStringUc encodes the given byte-buffer as base36 using [0-9A-Z] as
// the digit-alphabet
func EncodeToStringUc(b []byte) string { return encode(b, UcAlphabet) }

// EncodeToStringLc encodes the given byte-buffer as base36 using [0-9a-z] as
// the digit-alphabet
func EncodeToStringLc(b []byte) string { return encode(b, LcAlphabet) }

func encode(inBuf []byte, al string) string {

	// As a polar opposite to the base58 implementation, using a uint32 here is
	// significantly slower
	var carry uint64

	var encIdx, valIdx, zcnt, high int

	inSize := len(inBuf)
	for zcnt < inSize && inBuf[zcnt] == 0 {
		zcnt++
	}

	// Really this is log(256)/log(36) or 1.55, but integer math is easier
	// Use 2 as a constant and just overallocate
	encSize := (inSize - zcnt) * 2

	// Allocate one big buffer up front
	// Note: pools *DO NOT* help, the overhead of zeroing the val-half (see below)
	// kills any performance gain to be had
	outBuf := make([]byte, (zcnt + encSize*2))

	// use the second half for the temporary numeric buffer
	val := outBuf[encSize+zcnt:]

	high = encSize - 1
	for _, b := range inBuf[zcnt:] {
		valIdx = encSize - 1
		for carry = uint64(b); valIdx > high || carry != 0; valIdx-- {
			carry += uint64((val[valIdx])) * 256
			val[valIdx] = byte(carry % 36)
			carry /= 36
		}
		high = valIdx
	}

	// Reset the value index to the first significant value position
	for valIdx = 0; valIdx < encSize && val[valIdx] == 0; valIdx++ {
	}

	// Now write the known-length result to first half of buffer
	encSize += zcnt - valIdx

	for encIdx = 0; encIdx < zcnt; encIdx++ {
		outBuf[encIdx] = '0'
	}

	for encIdx < encSize {
		outBuf[encIdx] = al[val[valIdx]]
		encIdx++
		valIdx++
	}

	return string(outBuf[:encSize])
}

// DecodeString takes a base36 encoded string and returns a slice of the decoded
// bytes.
func DecodeString(s string) ([]byte, error) {

	if len(s) == 0 {
		return nil, fmt.Errorf("can not decode zero-length string")
	}

	var zcnt int

	for i := 0; i < len(s) && s[i] == '0'; i++ {
		zcnt++
	}

	var t, c uint64

	outi := make([]uint32, (len(s)+3)/4)
	binu := make([]byte, (len(s)+3)*3)

	for _, r := range s {
		if r > rune(maxDigitOrdinal) || revAlphabet[r] > maxDigitValueB36 {
			return nil, fmt.Errorf("invalid base36 character (%q)", r)
		}

		c = uint64(revAlphabet[r])

		for j := len(outi) - 1; j >= 0; j-- {
			t = uint64(outi[j])*36 + c
			c = (t >> 32)
			outi[j] = uint32(t & 0xFFFFFFFF)
		}

	}

	mask := (uint(len(s)%4) * 8)
	if mask == 0 {
		mask = 32
	}
	mask -= 8
	var j, cnt int
	for j, cnt = 0, 0; j < len(outi); j++ {
		for mask < 32 { // loop relies on uint overflow
			binu[cnt] = byte(outi[j] >> mask)
			mask -= 8
			cnt++
		}
		mask = 24
	}

	for n := zcnt; n < len(binu); n++ {
		if binu[n] > 0 {
			return binu[n-zcnt : cnt], nil
		}
	}
	return binu[:cnt], nil
}
