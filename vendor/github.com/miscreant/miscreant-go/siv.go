// Originally written in 2015 by Dmitry Chestnykh.
// Modified in 2017 by Tony Arcieri.
//
// Miscreant implements Synthetic Initialization Vector (SIV)-based
// authenticated encryption using the AES block cipher (RFC 5297).

package miscreant

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/subtle"
	"errors"
	"github.com/miscreant/miscreant-go/block"
	"github.com/miscreant/miscreant-go/cmac"
	"github.com/miscreant/miscreant-go/pmac"
	"hash"
)

// MaxAssociatedDataItems is the maximum number of associated data items
const MaxAssociatedDataItems = 126

var (
	// ErrKeySize indicates the given key size is not supported
	ErrKeySize = errors.New("siv: bad key size")

	// ErrNotAuthentic indicates a ciphertext is malformed or corrupt
	ErrNotAuthentic = errors.New("siv: authentication failed")

	// ErrTooManyAssociatedDataItems indicates more than MaxAssociatedDataItems were given
	ErrTooManyAssociatedDataItems = errors.New("siv: too many associated data items")
)

// Cipher is an instance of AES-SIV, configured with either AES-CMAC or
// AES-PMAC as a message authentication code.
type Cipher struct {
	// MAC function used to derive a synthetic IV and authenticate the message
	h hash.Hash

	// Block cipher function used to encrypt the message
	b cipher.Block

	// Internal buffers
	tmp1, tmp2 block.Block
}

// NewAESCMACSIV returns a new AES-SIV cipher with the given key, which must be
// twice as long as an AES key, either 32 or 64 bytes to select AES-128
// (AES-CMAC-SIV-256), or AES-256 (AES-CMAC-SIV-512).
func NewAESCMACSIV(key []byte) (c *Cipher, err error) {
	n := len(key)
	if n != 32 && n != 64 {
		return nil, ErrKeySize
	}

	macBlock, err := aes.NewCipher(key[:n/2])
	if err != nil {
		return nil, err
	}

	ctrBlock, err := aes.NewCipher(key[n/2:])
	if err != nil {
		return nil, err
	}

	c = new(Cipher)
	c.h = cmac.New(macBlock)
	c.b = ctrBlock

	return c, nil
}

// NewAESPMACSIV returns a new AES-SIV cipher with the given key, which must be
// twice as long as an AES key, either 32 or 64 bytes to select AES-128
// (AES-PMAC-SIV-256), or AES-256 (AES-PMAC-SIV-512).
func NewAESPMACSIV(key []byte) (c *Cipher, err error) {
	n := len(key)
	if n != 32 && n != 64 {
		return nil, ErrKeySize
	}

	macBlock, err := aes.NewCipher(key[:n/2])
	if err != nil {
		return nil, err
	}

	ctrBlock, err := aes.NewCipher(key[n/2:])
	if err != nil {
		return nil, err
	}

	c = new(Cipher)
	c.h = pmac.New(macBlock)
	c.b = ctrBlock

	return c, nil
}

// Overhead returns the difference between plaintext and ciphertext lengths.
func (c *Cipher) Overhead() int {
	return c.h.Size()
}

// Seal encrypts and authenticates plaintext, authenticates the given
// associated data items, and appends the result to dst, returning the updated
// slice.
//
// The plaintext and dst may alias exactly or not at all.
//
// For nonce-based encryption, the nonce should be the last associated data item.
func (c *Cipher) Seal(dst []byte, plaintext []byte, data ...[]byte) ([]byte, error) {
	if len(data) > MaxAssociatedDataItems {
		return nil, ErrTooManyAssociatedDataItems
	}

	// Authenticate
	iv := c.s2v(data, plaintext)
	ret, out := sliceForAppend(dst, len(iv)+len(plaintext))
	copy(out, iv)

	// Encrypt
	zeroIVBits(iv)
	ctr := cipher.NewCTR(c.b, iv)
	ctr.XORKeyStream(out[len(iv):], plaintext)

	return ret, nil
}

// Open decrypts ciphertext, authenticates the decrypted plaintext and the given
// associated data items and, if successful, appends the resulting plaintext
// to dst, returning the updated slice. The additional data items must match the
// items passed to Seal.
//
// The ciphertext and dst may alias exactly or not at all.
//
// For nonce-based encryption, the nonce should be the last associated data item.
func (c *Cipher) Open(dst []byte, ciphertext []byte, data ...[]byte) ([]byte, error) {
	if len(data) > MaxAssociatedDataItems {
		return nil, ErrTooManyAssociatedDataItems
	}
	if len(ciphertext) < c.Overhead() {
		return nil, ErrNotAuthentic
	}

	// Decrypt
	iv := c.tmp1[:c.Overhead()]
	copy(iv, ciphertext)
	zeroIVBits(iv)
	ctr := cipher.NewCTR(c.b, iv)
	ret, out := sliceForAppend(dst, len(ciphertext)-len(iv))
	ctr.XORKeyStream(out, ciphertext[len(iv):])

	// Authenticate
	expected := c.s2v(data, out)
	if subtle.ConstantTimeCompare(ciphertext[:len(iv)], expected) != 1 {
		return nil, ErrNotAuthentic
	}
	return ret, nil
}

func (c *Cipher) s2v(s [][]byte, sn []byte) []byte {
	h := c.h
	h.Reset()

	tmp, d := c.tmp1, c.tmp2
	tmp.Clear()

	// NOTE(dchest): The standalone S2V returns CMAC(1) if the number of
	// passed vectors is zero, however in SIV construction this case is
	// never triggered, since we always pass plaintext as the last vector
	// (even if it's zero-length), so we omit this case.

	_, err := h.Write(tmp[:])
	if err != nil {
		panic(err)
	}

	copy(d[:], h.Sum(d[:0]))
	h.Reset()

	for _, v := range s {
		_, err := h.Write(v)
		if err != nil {
			panic(err)
		}

		copy(tmp[:], h.Sum(tmp[:0]))
		h.Reset()
		d.Dbl()
		xor(d[:], tmp[:])
	}

	tmp.Clear()

	if len(sn) >= h.BlockSize() {
		n := len(sn) - len(d)
		copy(tmp[:], sn[n:])
		_, err = h.Write(sn[:n])
		if err != nil {
			panic(err)
		}
	} else {
		copy(tmp[:], sn)
		tmp[len(sn)] = 0x80
		d.Dbl()
	}
	xor(tmp[:], d[:])
	_, err = h.Write(tmp[:])
	if err != nil {
		panic(err)
	}

	return h.Sum(tmp[:0])
}

func xor(a, b []byte) {
	for i, v := range b {
		a[i] ^= v
	}
}

func zeroIVBits(iv []byte) {
	// "We zero-out the top bit in each of the last two 32-bit words
	// of the IV before assigning it to Ctr"
	//  — http://web.cs.ucdavis.edu/~rogaway/papers/siv.pdf
	iv[len(iv)-8] &= 0x7f
	iv[len(iv)-4] &= 0x7f
}

// sliceForAppend takes a slice and a requested number of bytes. It returns a
// slice with the contents of the given slice followed by that many bytes and a
// second slice that aliases into it and contains only the extra bytes. If the
// original slice has sufficient capacity then no allocation is performed.
func sliceForAppend(in []byte, n int) (head, tail []byte) {
	if total := len(in) + n; cap(in) >= total {
		head = in[:total]
	} else {
		head = make([]byte, total)
		copy(head, in)
	}
	tail = head[len(in):]
	return
}
