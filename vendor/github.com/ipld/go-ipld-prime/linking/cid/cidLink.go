package cidlink

import (
	"fmt"

	cid "github.com/ipfs/go-cid"
	ipld "github.com/ipld/go-ipld-prime"
	multihash "github.com/multiformats/go-multihash"
)

var (
	_ ipld.Link          = Link{}
	_ ipld.LinkPrototype = LinkPrototype{}
)

// Link implements the ipld.Link interface using a CID.
// See https://github.com/ipfs/go-cid for more information about CIDs.
//
// When using this value, typically you'll use it as `Link`, and not `*Link`.
// This includes when handling the value as an `ipld.Link` interface -- the non-pointer form is typically preferable.
// This is because the ipld.Link inteface is often desirable to be able to use as a golang map key,
// and in that context, pointers would not result in the desired behavior.
type Link struct {
	cid.Cid
}

func (lnk Link) Prototype() ipld.LinkPrototype {
	return LinkPrototype{lnk.Cid.Prefix()}
}
func (lnk Link) String() string {
	return lnk.Cid.String()
}

type LinkPrototype struct {
	cid.Prefix
}

func (lp LinkPrototype) BuildLink(hashsum []byte) ipld.Link {
	// Does this method body look surprisingly complex?  I agree.
	//  We actually have to do all this work.  The go-cid package doesn't expose a constructor that just lets us directly set the bytes and the prefix numbers next to each other.
	//  No, `cid.Prefix.Sum` is not the method you are looking for: that expects the whole data body.
	//  Most of the logic here is the same as the body of `cid.Prefix.Sum`; we just couldn't get at the relevant parts without copypasta.
	//  There is also some logic that's sort of folded in from the go-multihash module.  This is really a mess.
	//  The go-cid package needs review.  So does go-multihash.  Their responsibilies are not well compartmentalized and they don't play well with other stdlib golang interfaces.
	p := lp.Prefix

	length := p.MhLength
	if p.MhType == multihash.ID {
		length = -1
	}
	if p.Version == 0 && (p.MhType != multihash.SHA2_256 ||
		(p.MhLength != 32 && p.MhLength != -1)) {
		panic(fmt.Errorf("invalid cid v0 prefix"))
	}

	if length != -1 {
		hashsum = hashsum[:p.MhLength]
	}

	mh, err := multihash.Encode(hashsum, p.MhType)
	if err != nil {
		panic(err) // No longer possible, but multihash still returns an error for legacy reasons.
	}

	switch lp.Prefix.Version {
	case 0:
		return Link{cid.NewCidV0(mh)}
	case 1:
		return Link{cid.NewCidV1(p.Codec, mh)}
	default:
		panic(fmt.Errorf("invalid cid version"))
	}
}
