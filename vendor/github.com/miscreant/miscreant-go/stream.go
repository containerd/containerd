package miscreant

import (
	"crypto/cipher"
	"encoding/binary"
)

// streamNoncePrefixSize is the user-supplied nonce size
const streamNoncePrefixSize = 8

// streamExtendedNonceSize is the nonce prefix + 32-bit counter + 1-byte last block flag
const streamExtendedNonceSize = streamNoncePrefixSize + 4 + 1

// lastBlockFlag indicates that a block is the last in the STREAM
const lastBlockFlag byte = 1

// counterMax is the maximum allowable value for the stream counter
const counterMax uint64 = 0xFFFFFFFF

// StreamEncryptor encrypts message streams, selecting the nonces using a
// 32-bit counter, generalized for any cipher.AEAD algorithm
//
// This construction corresponds to the ℰ stream encryptor object as defined in
// the paper Online Authenticated-Encryption and its Nonce-Reuse Misuse-Resistance
type StreamEncryptor struct {
	// cipher.AEAD instance underlying this STREAM
	a cipher.AEAD

	// Nonce encoder instance which computes per-message nonces
	n *nonceEncoder32
}

// NewStreamEncryptor returns a STREAM encryptor instance  with the given
// cipher, nonce, and a key which must be twice as long  as an AES key, either
// 32 or 64 bytes to select AES-128 (AES-SIV-256)  or AES-256 (AES-SIV-512).
func NewStreamEncryptor(alg string, key, nonce []byte) (*StreamEncryptor, error) {
	aead, err := NewAEAD(alg, key, streamExtendedNonceSize)
	if err != nil {
		return nil, err
	}

	nonceEncoder, err := newNonceEncoder32(nonce)
	if err != nil {
		return nil, err
	}

	return &StreamEncryptor{a: aead, n: nonceEncoder}, nil
}

// NonceSize returns the size of the nonce that must be passed to
// NewStreamEncryptor
func (e *StreamEncryptor) NonceSize() int { return streamNoncePrefixSize }

// Overhead returns the maximum difference between the lengths of a
// plaintext and its ciphertext, which in the case of AES-SIV modes
// is the size of the initialization vector
func (e *StreamEncryptor) Overhead() int { return e.a.Overhead() }

// Seal the next message in the STREAM, which encrypts and authenticates
// plaintext, authenticates the additional data and appends the result to dst,
// returning the updated slice.
//
// The plaintext and dst may alias exactly or not at all. To reuse
// plaintext's storage for the encrypted output, use plaintext[:0] as dst.
//
// The lastBlock argument should be set to true if this is the last message
// in the STREAM. No further messages can be encrypted after the last one
func (e *StreamEncryptor) Seal(dst, plaintext, aData []byte, lastBlock bool) []byte {
	return e.a.Seal(dst, e.n.Next(lastBlock), plaintext, aData)
}

// StreamDecryptor decrypts message streams, selecting the nonces using a
// 32-bit counter, generalized for any cipher.AEAD algorithm
//
// This construction corresponds to the ℰ stream encryptor object as defined in
// the paper Online Authenticated-Encryption and its Nonce-Reuse Misuse-Resistance
type StreamDecryptor struct {
	// cipher.AEAD instance underlying this STREAM
	a cipher.AEAD

	// Nonce encoder instance which computes per-message nonces
	n *nonceEncoder32
}

// NewStreamDecryptor returns a STREAM encryptor instance  with the given
// cipher, nonce, and a key which must be twice as long  as an AES key, either
// 32 or 64 bytes to select AES-128 (AES-SIV-256)  or AES-256 (AES-SIV-512).
func NewStreamDecryptor(alg string, key, nonce []byte) (*StreamDecryptor, error) {
	aead, err := NewAEAD(alg, key, streamExtendedNonceSize)
	if err != nil {
		return nil, err

	}

	nonceEncoder, err := newNonceEncoder32(nonce)
	if err != nil {
		return nil, err
	}

	return &StreamDecryptor{a: aead, n: nonceEncoder}, nil
}

// NonceSize returns the size of the nonce that must be passed to
// NewStreamDecryptor
func (d *StreamDecryptor) NonceSize() int { return streamNoncePrefixSize }

// Overhead returns the maximum difference between the lengths of a
// plaintext and its ciphertext, which in the case of AES-SIV modes
// is the size of the initialization vector
func (d *StreamDecryptor) Overhead() int { return d.a.Overhead() }

// Open decrypts and authenticates the next ciphertext in the STREAM,
// and also authenticates the additional data, ensuring it matches
// the value passed to Seal.
//
// If successful, it appends the resulting plaintext to dst and returns
// the updated slice.
//
// The ciphertext and dst may alias exactly or not at all. To reuse
// ciphertext's storage for the decrypted output, use ciphertext[:0] as dst.
//
// Even if the function fails, the contents of dst, up to its capacity,
// may be overwritten.
func (d *StreamDecryptor) Open(dst, ciphertext, aData []byte, lastBlock bool) ([]byte, error) {
	return d.a.Open(dst, d.n.Next(lastBlock), ciphertext, aData)
}

// Computes STREAM nonces based on the current position in the STREAM.
//
// Accepts a 64-bit nonce and uses a 32-bit counter internally.
//
// Panics if the nonce size is incorrect, or the 32-bit counter overflows
type nonceEncoder32 struct {
	value    [streamExtendedNonceSize]byte
	counter  uint64
	finished bool
}

func newNonceEncoder32(noncePrefix []byte) (*nonceEncoder32, error) {
	if len(noncePrefix) != streamNoncePrefixSize {
		panic("miscreant.STREAM: incorrect nonce length")
	}

	value := [streamExtendedNonceSize]byte{0}
	copy(value[:streamNoncePrefixSize], noncePrefix)

	return &nonceEncoder32{
		value:    value,
		counter:  0,
		finished: false,
	}, nil
}

func (n *nonceEncoder32) Next(lastBlock bool) []byte {
	if n.finished {
		panic("miscreant.STREAM: already finished")
	}

	counterSlice := n.value[streamNoncePrefixSize : streamNoncePrefixSize+4]
	binary.BigEndian.PutUint32(counterSlice, uint32(n.counter))

	if lastBlock {
		n.value[len(n.value)-1] = lastBlockFlag
		n.finished = true
	} else {
		n.counter++
		if n.counter > counterMax {
			panic("miscreant.STREAM: nonce counter overflowed")
		}
	}

	return n.value[:]
}
