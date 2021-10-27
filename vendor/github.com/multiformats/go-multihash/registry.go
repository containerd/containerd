package multihash

import (
	"hash"

	mhreg "github.com/multiformats/go-multihash/core"

	_ "github.com/multiformats/go-multihash/register/all"
	_ "github.com/multiformats/go-multihash/register/miniosha256"
)

// Register is an alias for Register in the core package.
//
// Consider using the core package instead of this multihash package;
// that package does not introduce transitive dependencies except for those you opt into,
// and will can result in smaller application builds.
func Register(indicator uint64, hasherFactory func() hash.Hash) {
	mhreg.Register(indicator, hasherFactory)
}

// Register is an alias for Register in the core package.
//
// Consider using the core package instead of this multihash package;
// that package does not introduce transitive dependencies except for those you opt into,
// and will can result in smaller application builds.
func GetHasher(indicator uint64) (hash.Hash, error) {
	return mhreg.GetHasher(indicator)
}

// DefaultLengths maps a multihash indicator code to the output size for that hash, in units of bytes.
var DefaultLengths = mhreg.DefaultLengths
