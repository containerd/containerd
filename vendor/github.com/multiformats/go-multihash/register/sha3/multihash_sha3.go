/*
	This package has no purpose except to perform registration of multihashes.

	It is meant to be used as a side-effecting import, e.g.

		import (
			_ "github.com/multiformats/go-multihash/register/sha3"
		)

	This package registers several multihashes for the sha3 family.
	This also includes some functions known as "shake" and "keccak",
	since they share much of their implementation and come in the same repos.
*/
package sha3

import (
	"hash"

	"golang.org/x/crypto/sha3"

	"github.com/multiformats/go-multihash/core"
)

func init() {
	multihash.Register(multihash.SHA3_512, sha3.New512)
	multihash.Register(multihash.SHA3_384, sha3.New384)
	multihash.Register(multihash.SHA3_256, sha3.New256)
	multihash.Register(multihash.SHA3_224, sha3.New224)
	multihash.Register(multihash.SHAKE_128, func() hash.Hash { return shakeNormalizer{sha3.NewShake128(), 128 / 8 * 2} })
	multihash.Register(multihash.SHAKE_256, func() hash.Hash { return shakeNormalizer{sha3.NewShake256(), 256 / 8 * 2} })
	multihash.Register(multihash.KECCAK_256, sha3.NewLegacyKeccak256)
	multihash.Register(multihash.KECCAK_512, sha3.NewLegacyKeccak512)
}

// sha3.ShakeHash presents a somewhat odd interface, and requires a wrapper to normalize it to the usual hash.Hash interface.
//
// Some of the fiddly bits required by this normalization probably makes it undesirable for use in the highest performance applications;
// There's at least one extra allocation in constructing it (sha3.ShakeHash is an interface, so that's one heap escape; and there's a second heap escape when this normalizer struct gets boxed into a hash.Hash interface),
// and there's at least one extra allocation in getting a sum out of it (because reading a shake hash is a mutation (!) and the API only provides cloning as a way to escape this).
// Fun.
type shakeNormalizer struct {
	sha3.ShakeHash
	size int
}

func (shakeNormalizer) BlockSize() int {
	return 32 // Shake doesn't have a prefered block size, apparently.  An arbitrary but unsurprising and positive nonzero number has been chosen to minimize the odds of fascinating bugs.
}

func (x shakeNormalizer) Size() int {
	return x.size
}

func (x shakeNormalizer) Sum(digest []byte) []byte {
	if len(digest) < x.size {
		digest = make([]byte, x.size)
	}
	digest = digest[0:x.size]
	h2 := x.Clone() // clone it, because reading mutates this kind of hash (!) which is not the standard contract for a Hash.Sum method.
	h2.Read(digest) // not capable of underreading.  See sha3.ShakeSum256 for similar usage.
	return digest
}
