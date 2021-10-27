package codec

import (
	"github.com/ipld/go-ipld-prime"
)

// Encoder is defined in the root ipld package; this alias is just for documentation and discoverability.
//
// Encoder functions can be composed into an ipld.LinkSystem to provide
// a "one stop shop" API for handling content addressable storage.
// Encoder functions can also be used directly if you want to handle serial data streams.
//
// Most codec packages will have a ReusableEncoder type
// (which contains any working memory needed by the encoder implementation,
// as well as any configuration options),
// and that type will have an Encode function matching this interface.
//
// By convention, codec packages that have a multicodec contract will also have
// a package-scope exported function called Encode which also matches this interface,
// and is the equivalent of creating a zero-value ReusableEncoder (aka, default config)
// and using its Encode method.
// This package-scope function will typically also internally use a sync.Pool
// to keep some ReusableEncoder values on hand to avoid unnecesary allocations.
//
// Note that a ReusableEncoder type that supports configuration options
// does not functionally expose those options when invoked by the multicodec system --
// multicodec indicators do not provide room for extended configuration info.
// Codecs that expose configuration options are doing so for library users to enjoy;
// it does not mean those non-default configurations will necessarly be available
// in all scenarios that use codecs indirectly.
// There is also no standard interface for such configurations: by nature,
// if they exist at all, they vary per codec.
type Encoder = ipld.Encoder

// Decoder is defined in the root ipld package; this alias is just for documentation and discoverability.
//
// Most of the documentation for Encoder also applies wholesale to the Decoder interface.
type Decoder = ipld.Decoder

type ErrBudgetExhausted struct{}

func (e ErrBudgetExhausted) Error() string {
	return "decoder resource budget exhausted (message too long or too complex)"
}

type MapSortMode uint8

const (
	MapSortMode_None MapSortMode = iota
	MapSortMode_Lexical
	MapSortMode_RFC7049
)
