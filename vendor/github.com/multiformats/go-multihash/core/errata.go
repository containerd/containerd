package multihash

import (
	"bytes"
	"crypto/sha256"
	"hash"
)

type identityMultihash struct {
	bytes.Buffer
}

func (identityMultihash) BlockSize() int {
	return 32 // A prefered block size is nonsense for the "identity" "hash".  An arbitrary but unsurprising and positive nonzero number has been chosen to minimize the odds of fascinating bugs.
}

func (x *identityMultihash) Size() int {
	return x.Len()
}

func (x *identityMultihash) Sum(digest []byte) []byte {
	return x.Bytes()
}

type doubleSha256 struct {
	hash.Hash
}

func (x doubleSha256) Sum(digest []byte) []byte {
	digest = x.Hash.Sum(digest)
	h2 := sha256.New()
	h2.Write(digest)
	return h2.Sum(digest[0:0])
}
