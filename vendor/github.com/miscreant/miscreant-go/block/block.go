// Common block cipher functionality shared across this library

package block

import (
	"crypto/cipher"
	"crypto/subtle"
)

const (
	// Size of an AES block in bytes
	Size = 16

	// R is the minimal irreducible polynomial for a 128-bit block size
	R = 0x87
)

// Block is a 128-bit array used by certain block ciphers (i.e. AES)
type Block [Size]byte

// Clear zeroes out the contents of the block
func (b *Block) Clear() {
	// TODO: use a more secure zeroing method that won't be optimized away
	for i := range b {
		b[i] = 0
	}
}

// Dbl performs a doubling of a block over GF(2^128):
//
//     a<<1 if firstbit(a)=0
//     (a<<1) ⊕ 0¹²⁰10000111 if firstbit(a)=1
//
func (b *Block) Dbl() {
	var z byte

	for i := Size - 1; i >= 0; i-- {
		zz := b[i] >> 7
		b[i] = b[i]<<1 | z
		z = zz
	}

	b[Size-1] ^= byte(subtle.ConstantTimeSelect(int(z), R, 0))
}

// Encrypt a block with the given block cipher
func (b *Block) Encrypt(c cipher.Block) {
	c.Encrypt(b[:], b[:])
}
