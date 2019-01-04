// PMAC message authentication code, defined in
// http://web.cs.ucdavis.edu/~rogaway/ocb/pmac.pdf

package pmac

import (
	"crypto/cipher"
	"crypto/subtle"
	"hash"
	"math/bits"

	"github.com/miscreant/miscreant-go/block"
)

// Number of L blocks to precompute (i.e. µ in the PMAC paper)
// TODO: dynamically compute these as needed
const precomputedBlocks = 31

type pmac struct {
	// c is the block cipher we're using (i.e. AES-128 or AES-256)
	c cipher.Block

	// l is defined as follows (quoted from the PMAC paper):
	//
	// Equation 1:
	//
	//     a · x =
	//         a<<1 if firstbit(a)=0
	//         (a<<1) ⊕ 0¹²⁰10000111 if firstbit(a)=1
	//
	// Equation 2:
	//
	//     a · x⁻¹ =
	//         a>>1 if lastbit(a)=0
	//         (a>>1) ⊕ 10¹²⁰1000011 if lastbit(a)=1
	//
	// Let L(0) ← L. For i ∈ [1..µ], compute L(i) ← L(i − 1) · x by
	// Equation (1) using a shift and a conditional xor.
	//
	// Compute L(−1) ← L · x⁻¹ by Equation (2), using a shift and a
	// conditional xor.
	//
	// Save the values L(−1), L(0), L(1), L(2), ..., L(µ) in a table.
	// (Alternatively, [ed: as we have done in this codebase] defer computing
	// some or  all of these L(i) values until the value is actually needed.)
	l [precomputedBlocks]block.Block

	// lInv contains the multiplicative inverse (i.e. right shift) of the first
	// l-value, computed as described above, and is XORed into the tag in the
	// event the message length is a multiple of the block size
	lInv block.Block

	// digest contains the PMAC tag-in-progress
	digest block.Block

	// offset is a block specific tweak to the input message
	offset block.Block

	// buf contains a part of the input message, processed a block-at-a-time
	buf block.Block

	// pos marks the end of plaintext in the buf
	pos uint

	// ctr is the number of blocks we have MAC'd so far
	ctr uint

	// finished is set true when we are done processing a message, and forbids
	// any subsequent writes until we reset the internal state
	finished bool
}

// New creates a new PMAC instance using the given cipher
func New(c cipher.Block) hash.Hash {
	if c.BlockSize() != block.Size {
		panic("pmac: invalid cipher block size")
	}

	d := new(pmac)
	d.c = c

	var tmp block.Block
	tmp.Encrypt(c)

	for i := range d.l {
		copy(d.l[i][:], tmp[:])
		tmp.Dbl()
	}

	// Compute L(−1) ← L · x⁻¹:
	//
	//     a>>1 if lastbit(a)=0
	//     (a>>1) ⊕ 10¹²⁰1000011 if lastbit(a)=1
	//
	copy(tmp[:], d.l[0][:])
	lastBit := int(tmp[block.Size-1] & 0x01)

	for i := block.Size - 1; i > 0; i-- {
		carry := byte(subtle.ConstantTimeSelect(int(tmp[i-1]&1), 0x80, 0))
		tmp[i] = (tmp[i] >> 1) | carry
	}

	tmp[0] >>= 1
	tmp[0] ^= byte(subtle.ConstantTimeSelect(lastBit, 0x80, 0))
	tmp[block.Size-1] ^= byte(subtle.ConstantTimeSelect(lastBit, block.R>>1, 0))
	copy(d.lInv[:], tmp[:])

	return d
}

// Reset clears the digest state, starting a new digest.
func (d *pmac) Reset() {
	d.digest.Clear()
	d.offset.Clear()
	d.buf.Clear()
	d.pos = 0
	d.ctr = 0
	d.finished = false
}

// Write adds the given data to the digest state.
func (d *pmac) Write(msg []byte) (int, error) {
	if d.finished {
		panic("pmac: already finished")
	}

	var msgPos, msgLen, remaining uint
	msgLen = uint(len(msg))
	remaining = block.Size - d.pos

	// Finish filling the internal buf with the message
	if msgLen > remaining {
		copy(d.buf[d.pos:], msg[:remaining])

		msgPos += remaining
		msgLen -= remaining

		d.processBuffer()
	}

	// So long as we have more than a blocks worth of data, compute
	// whole-sized blocks at a time.
	for msgLen > block.Size {
		copy(d.buf[:], msg[msgPos:msgPos+block.Size])

		msgPos += block.Size
		msgLen -= block.Size

		d.processBuffer()
	}

	if msgLen > 0 {
		copy(d.buf[d.pos:d.pos+msgLen], msg[msgPos:])
		d.pos += msgLen
	}

	return len(msg), nil
}

// Sum returns the PMAC digest, one cipher block in length,
// of the data written with Write.
func (d *pmac) Sum(in []byte) []byte {
	if d.finished {
		panic("pmac: already finished")
	}

	if d.pos == block.Size {
		xor(d.digest[:], d.buf[:])
		xor(d.digest[:], d.lInv[:])
	} else {
		xor(d.digest[:], d.buf[:d.pos])
		d.digest[d.pos] ^= 0x80
	}

	d.digest.Encrypt(d.c)
	d.finished = true

	return append(in, d.digest[:]...)
}

func (d *pmac) Size() int { return block.Size }

func (d *pmac) BlockSize() int { return block.Size }

// Update the internal tag state based on the buf contents
func (d *pmac) processBuffer() {
	xor(d.offset[:], d.l[bits.TrailingZeros(d.ctr+1)][:])
	xor(d.buf[:], d.offset[:])
	d.ctr++

	d.buf.Encrypt(d.c)
	xor(d.digest[:], d.buf[:])
	d.pos = 0
}

// XOR the contents of b into a in-place
func xor(a, b []byte) {
	for i, v := range b {
		a[i] ^= v
	}
}
