/*
	This package has no purpose except to perform registration of multihashes.

	It is meant to be used as a side-effecting import, e.g.

		import (
			_ "github.com/multiformats/go-multihash/register/blake2"
		)

	This package registers several multihashes for the blake2 family
	(both the 's' and the 'b' variants, and in a variety of sizes).
*/
package blake2

import (
	"hash"

	"github.com/minio/blake2b-simd"
	"golang.org/x/crypto/blake2s"

	"github.com/multiformats/go-multihash/core"
)

const (
	blake2b_min = 0xb201
	blake2b_max = 0xb240
	blake2s_min = 0xb241
	blake2s_max = 0xb260
)

func init() {
	// blake2s
	// This package only enables support for 32byte (256 bit) blake2s.
	multihash.Register(blake2s_min+31, func() hash.Hash { h, _ := blake2s.New256(nil); return h })

	// blake2b
	// There's a whole range of these.
	for c := uint64(blake2b_min); c <= blake2b_max; c++ {
		size := int(c - blake2b_min + 1)

		// special case these lengths to avoid allocations.
		switch size {
		case 32:
			multihash.Register(c, blake2b.New256)
			continue
		case 64:
			multihash.Register(c, blake2b.New512)
			continue
		}

		// Ok, allocate away.
		//  (The config object here being a pointer is a tad unfortunate,
		//   but we manage amortize it away by making them just once anyway.)
		cfg := &blake2b.Config{Size: uint8(size)}
		multihash.Register(c, func() hash.Hash {
			hasher, err := blake2b.New(cfg)
			if err != nil {
				panic(err)
			}
			return hasher
		})
	}
}
