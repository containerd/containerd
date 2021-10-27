package keytransform

import ds "github.com/ipfs/go-datastore"

// Pair is a convince struct for constructing a key transform.
type Pair struct {
	Convert KeyMapping
	Invert  KeyMapping
}

func (t *Pair) ConvertKey(k ds.Key) ds.Key {
	return t.Convert(k)
}

func (t *Pair) InvertKey(k ds.Key) ds.Key {
	return t.Invert(k)
}

var _ KeyTransform = (*Pair)(nil)

// PrefixTransform constructs a KeyTransform with a pair of functions that
// add or remove the given prefix key.
//
// Warning: will panic if prefix not found when it should be there. This is
// to avoid insidious data inconsistency errors.
type PrefixTransform struct {
	Prefix ds.Key
}

// ConvertKey adds the prefix.
func (p PrefixTransform) ConvertKey(k ds.Key) ds.Key {
	return p.Prefix.Child(k)
}

// InvertKey removes the prefix. panics if prefix not found.
func (p PrefixTransform) InvertKey(k ds.Key) ds.Key {
	if p.Prefix.String() == "/" {
		return k
	}

	if !p.Prefix.IsAncestorOf(k) {
		panic("expected prefix not found")
	}

	s := k.String()[len(p.Prefix.String()):]
	return ds.RawKey(s)
}

var _ KeyTransform = (*PrefixTransform)(nil)
