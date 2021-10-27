Why does this package exist?
----------------------------

The `linking/cid` package bends the `github.com/ipfs/go-cid` package into conforming to the `ipld.Link` interface.

The `linking/cid` package also contains factory functions for `ipld.LinkSystem`.
These LinkSystem will be constructed with `EncoderChooser`, `DecoderChooser`, and `HasherChooser` funcs
which will use multicodec registries and multihash registries respectively.

### Why not use go-cid directly?

We need a "Link" interface in the root `ipld` package or things just aren't definable.
But we don't want the root `ipld.Link` concept to directly map to `go-cid.Cid` for several reasons:

1. We might want to revisit the go-cid library.  Possibly in the "significantly breaking changes" sense.
	- It's also not clear when we might do this -- and if we do, the transition period will be *long* because it's a highly-depended-upon library.
	- See below for some links to a gist that discusses why.
2. We might want to extend the concept of linking to more than just plain CIDs.
	- This is hypothetical at present -- but an often-discussed example is "what if CID+Path was also a Link?"
3. We might sometimes want to use IPLD libraries without using any CID implementation at all.
	- e.g. it's totally believable to want to use IPLD libraries for handling JSON and CBOR, even if you don't want IPLD linking.
	- if the CID packages were cheap enough, maybe this concern would fade -- but right now, they're **definitely** not; the transitive dependency tree of go-cid is *huge*.

#### If go-cid is revisited, what might that look like?

No idea.  (At least, not in a committal way.)

https://gist.github.com/warpfork/e871b7fee83cb814fb1f043089983bb3#existing-implementations
gathers some reflections on the problems that would be nice to solve, though.

https://gist.github.com/warpfork/e871b7fee83cb814fb1f043089983bb3#file-cid-go
contains a draft outline of what a revisited API could look like,
but note that at the time of writing, it is not strongly ratified nor in any way committed to.

At any rate, though, the operative question for this package is:
if we do revisit go-cid, how are we going to make the transition managable?

It seems unlikely we'd be able to make the transition manageable without some interface, somewhere.
So we might as well draw that line at `ipld.Link`.

(I hypothesize that a transition story might involve two CID packages,
which could grow towards a shared interface,
doing so in a way that's purely additive in the established `go-cid` package.
We'd need two separate go modules to do this, since the aim is reducing dependency bloat for those that use the new one.
The shared interface in this story could have more info than `ipld.Link` does now,
but would nonetheless still certainly be an interface in order to support the separation of modules.)

### Why are LinkSystem factory functions here, instead of in the main IPLD package?

Same reason as why we don't use go-cid directly.

If we put these LinkSystem defaults in the root `ipld` package,
we'd bring on all the transitive dependencies of `go-cid` onto an user of `ipld` unconditionally...
and we don't want to do that.

You know that Weird Al song "It's all about the pentiums"?
Retune that in your mind to "It's all about dependencies".
