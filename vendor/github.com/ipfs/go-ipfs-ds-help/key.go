// Package dshelp provides utilities for parsing and creating
// datastore keys used by go-ipfs
package dshelp

import (
	cid "github.com/ipfs/go-cid"
	"github.com/ipfs/go-datastore"
	"github.com/multiformats/go-base32"
)

// NewKeyFromBinary creates a new key from a byte slice.
func NewKeyFromBinary(rawKey []byte) datastore.Key {
	buf := make([]byte, 1+base32.RawStdEncoding.EncodedLen(len(rawKey)))
	buf[0] = '/'
	base32.RawStdEncoding.Encode(buf[1:], rawKey)
	return datastore.RawKey(string(buf))
}

// BinaryFromDsKey returns the byte slice corresponding to the given Key.
func BinaryFromDsKey(k datastore.Key) ([]byte, error) {
	return base32.RawStdEncoding.DecodeString(k.String()[1:])
}

// CidToDsKey creates a Key from the given Cid.
func CidToDsKey(k cid.Cid) datastore.Key {
	return NewKeyFromBinary(k.Bytes())
}

// DsKeyToCid converts the given Key to its corresponding Cid.
func DsKeyToCid(dsKey datastore.Key) (cid.Cid, error) {
	kb, err := BinaryFromDsKey(dsKey)
	if err != nil {
		return cid.Cid{}, err
	}
	return cid.Cast(kb)
}
