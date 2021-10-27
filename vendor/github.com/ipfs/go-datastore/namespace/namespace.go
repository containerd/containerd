package namespace

import (
	ds "github.com/ipfs/go-datastore"
	ktds "github.com/ipfs/go-datastore/keytransform"
)

// PrefixTransform constructs a KeyTransform with a pair of functions that
// add or remove the given prefix key.
//
// Warning: will panic if prefix not found when it should be there. This is
// to avoid insidious data inconsistency errors.
//
// DEPRECATED: Use ktds.PrefixTransform directly.
func PrefixTransform(prefix ds.Key) ktds.PrefixTransform {
	return ktds.PrefixTransform{Prefix: prefix}
}

// Wrap wraps a given datastore with a key-prefix.
func Wrap(child ds.Datastore, prefix ds.Key) *ktds.Datastore {
	if child == nil {
		panic("child (ds.Datastore) is nil")
	}

	return ktds.Wrap(child, PrefixTransform(prefix))
}
