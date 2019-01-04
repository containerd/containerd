// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// CMAC message authentication code, defined in
// NIST Special Publication SP 800-38B.

package cmac

import (
	"crypto/cipher"
	"hash"

	"github.com/miscreant/miscreant-go/block"
)

type cmac struct {
	// c is the block cipher we're using (i.e. AES-128 or AES-256)
	c cipher.Block

	// k1 and k2 are CMAC subkeys (for finishing the tag)
	k1, k2 block.Block

	// digest contains the PMAC tag-in-progress
	digest block.Block

	// buffer contains a part of the input message, processed a block-at-a-time
	buf block.Block

	// pos marks the end of plaintext in the buffer
	pos uint
}

// New returns a new instance of a CMAC message authentication code
// digest using the given cipher.Block.
func New(c cipher.Block) hash.Hash {
	if c.BlockSize() != block.Size {
		panic("pmac: invalid cipher block size")
	}

	d := new(cmac)
	d.c = c

	// Subkey generation, p. 7
	d.k1.Encrypt(c)
	d.k1.Dbl()

	copy(d.k2[:], d.k1[:])
	d.k2.Dbl()

	return d
}

// Reset clears the digest state, starting a new digest.
func (d *cmac) Reset() {
	d.digest.Clear()
	d.buf.Clear()
	d.pos = 0
}

// Write adds the given data to the digest state.
func (d *cmac) Write(p []byte) (nn int, err error) {
	nn = len(p)
	left := block.Size - d.pos

	if uint(len(p)) > left {
		xor(d.buf[d.pos:], p[:left])
		p = p[left:]
		d.buf.Encrypt(d.c)
		d.pos = 0
	}

	for uint(len(p)) > block.Size {
		xor(d.buf[:], p[:block.Size])
		p = p[block.Size:]
		d.buf.Encrypt(d.c)
	}

	if len(p) > 0 {
		xor(d.buf[d.pos:], p)
		d.pos += uint(len(p))
	}
	return
}

// Sum returns the CMAC digest, one cipher block in length,
// of the data written with Write.
func (d *cmac) Sum(in []byte) []byte {
	// Finish last block, mix in key, encrypt.
	// Don't edit ci, in case caller wants
	// to keep digesting after call to Sum.
	k := d.k1
	if d.pos < uint(len(d.digest)) {
		k = d.k2
	}
	for i := 0; i < len(d.buf); i++ {
		d.digest[i] = d.buf[i] ^ k[i]
	}
	if d.pos < uint(len(d.digest)) {
		d.digest[d.pos] ^= 0x80
	}
	d.digest.Encrypt(d.c)
	return append(in, d.digest[:]...)
}

func (d *cmac) Size() int { return len(d.digest) }

func (d *cmac) BlockSize() int { return d.c.BlockSize() }

func xor(a, b []byte) {
	for i, v := range b {
		a[i] ^= v
	}
}
