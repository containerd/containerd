package verifcid

import (
	"fmt"

	cid "github.com/ipfs/go-cid"
	mh "github.com/multiformats/go-multihash"
)

var ErrPossiblyInsecureHashFunction = fmt.Errorf("potentially insecure hash functions not allowed")
var ErrBelowMinimumHashLength = fmt.Errorf("hashes must be at %d least bytes long", minimumHashLength)

const minimumHashLength = 20

var goodset = map[uint64]bool{
	mh.SHA2_256:     true,
	mh.SHA2_512:     true,
	mh.SHA3_224:     true,
	mh.SHA3_256:     true,
	mh.SHA3_384:     true,
	mh.SHA3_512:     true,
	mh.SHAKE_256:    true,
	mh.DBL_SHA2_256: true,
	mh.KECCAK_224:   true,
	mh.KECCAK_256:   true,
	mh.KECCAK_384:   true,
	mh.KECCAK_512:   true,
	mh.ID:           true,

	mh.SHA1: true, // not really secure but still useful
}

func IsGoodHash(code uint64) bool {
	good, found := goodset[code]
	if good {
		return true
	}

	if !found {
		if code >= mh.BLAKE2B_MIN+19 && code <= mh.BLAKE2B_MAX {
			return true
		}
		if code >= mh.BLAKE2S_MIN+19 && code <= mh.BLAKE2S_MAX {
			return true
		}
	}

	return false
}

func ValidateCid(c cid.Cid) error {
	pref := c.Prefix()
	if !IsGoodHash(pref.MhType) {
		return ErrPossiblyInsecureHashFunction
	}

	if pref.MhType != mh.ID && pref.MhLength < minimumHashLength {
		return ErrBelowMinimumHashLength
	}

	return nil
}
