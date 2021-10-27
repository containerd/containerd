package cidlink

import (
	"fmt"
	"hash"

	"github.com/multiformats/go-multihash/core"

	"github.com/ipld/go-ipld-prime"
	"github.com/ipld/go-ipld-prime/multicodec"
)

// DefaultLinkSystem returns an ipld.LinkSystem which uses cidlink.Link for ipld.Link.
// During selection of encoders, decoders, and hashers, it examines the multicodec indicator numbers and multihash indicator numbers from the CID,
// and uses the default global multicodec registry (see the go-ipld-prime/multicodec package) for resolving codec implementations,
// and the default global multihash registry (see the go-multihash/core package) for resolving multihash implementations.
//
// No storage functions are present in the returned LinkSystem.
// The caller can assign those themselves as desired.
func DefaultLinkSystem() ipld.LinkSystem {
	return LinkSystemUsingMulticodecRegistry(multicodec.DefaultRegistry)
}

// LinkSystemUsingMulticodecRegistry is similar to DefaultLinkSystem, but accepts a multicodec.Registry as a parameter.
//
// This can help create a LinkSystem which uses different multicodec implementations than the global registry.
// (Sometimes this can be desired if you want some parts of a program to support a more limited suite of codecs than other parts of the program,
// or needed to use a different multicodec registry than the global one for synchronization purposes, or etc.)
func LinkSystemUsingMulticodecRegistry(mcReg multicodec.Registry) ipld.LinkSystem {
	return ipld.LinkSystem{
		EncoderChooser: func(lp ipld.LinkPrototype) (ipld.Encoder, error) {
			switch lp2 := lp.(type) {
			case LinkPrototype:
				fn, err := mcReg.LookupEncoder(lp2.GetCodec())
				if err != nil {
					return nil, err
				}
				return fn, nil
			default:
				return nil, fmt.Errorf("this encoderChooser can only handle cidlink.LinkPrototype; got %T", lp)
			}
		},
		DecoderChooser: func(lnk ipld.Link) (ipld.Decoder, error) {
			lp := lnk.Prototype()
			switch lp2 := lp.(type) {
			case LinkPrototype:
				fn, err := mcReg.LookupDecoder(lp2.GetCodec())
				if err != nil {
					return nil, err
				}
				return fn, nil
			default:
				return nil, fmt.Errorf("this decoderChooser can only handle cidlink.LinkPrototype; got %T", lp)
			}
		},
		HasherChooser: func(lp ipld.LinkPrototype) (hash.Hash, error) {
			switch lp2 := lp.(type) {
			case LinkPrototype:
				h, err := multihash.GetHasher(lp2.MhType)
				if err != nil {
					return nil, fmt.Errorf("no hasher registered for multihash indicator 0x%x: %w", lp2.MhType, err)
				}
				return h, nil
			default:
				return nil, fmt.Errorf("this hasherChooser can only handle cidlink.LinkPrototype; got %T", lp)
			}
		},
	}
}
