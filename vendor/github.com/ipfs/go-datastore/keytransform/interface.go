package keytransform

import ds "github.com/ipfs/go-datastore"

// KeyMapping is a function that maps one key to annother
type KeyMapping func(ds.Key) ds.Key

// KeyTransform is an object with a pair of functions for (invertibly)
// transforming keys
type KeyTransform interface {
	ConvertKey(ds.Key) ds.Key
	InvertKey(ds.Key) ds.Key
}
