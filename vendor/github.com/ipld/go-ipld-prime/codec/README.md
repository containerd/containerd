Codecs
======

The `go-ipld-prime/codec` package is a grouping package.
The subpackages contains some codecs which reside in this repo.

The codecs included here are our "batteries included" codecs,
but they are not otherwise special.

It is not necessary for a codec to be a subpackage here to be a valid codec to use with go-ipld-prime;
anything that implements the `ipld.Encoder` and `ipld.Decoder` interfaces is fine.


Terminology
-----------

We generally refer to "codecs" as having an "encode" function and "decode" function.

We consider "encoding" to be the process of going from {Data Model} to {serial data},
and "decoding" to be the process of going from {serial data} to {Data Model}.

### Codec vs Multicodec

A "codec" is _any_ function that goes from {Data Model} to {serial data}, or vice versa.

A "multicodec" is a function which does that and is _also_ specifically recognized and described in
the tables in https://github.com/multiformats/multicodec/ .

Multicodecs generally leave no further room for customization and configuration,
because their entire behavior is supposed to be specified by a multicodec indicator code number.

Our codecs, in the child packages of this one, usually offer configuration options.
They also usually offer exactly one function, which does *not* allow configuration,
which is supplying a multicodec-compatible behavior.
You'll see this marked in the docs on those functions.

### Marshal vs Encode

It's common to see the terms "marshal" and "unmarshal" used in golang.

Those terms are usually describing when structured data is transformed into linearized, tokenized data
(and then, perhaps, all the way to serially encoded data), or vice versa.

We would use the words the same way... except we don't end up using them,
because that feature doesn't really come up in our codec layer.

In IPLD, we would describe mapping some typed data into Data Model as "marshalling".
(It's one step shy of tokenizing, but barely: Data Model does already have defined ordering for every element of data.)
And we do have systems that do this:
`bindnode` and our codegen systems both do this, implicitly, when they give you an `ipld.Node` of the representation of some data.

We just don't end up talking about it as "marshalling" because of how it's done implicitly by those systems.
As a result, all of our features relating to codecs only end up speaking about "encoding" and "decoding".

### Legacy code

There are some appearances of the words "marshal" and "unmarshal" in some of our subpackages here.

That verbiage is generally on the way out.
For functions and structures with those names, you'll notice their docs marking them as deprecated.


Why have "batteries-included" codecs?
-------------------------------------

These codecs live in this repo because they're commonly used, highly supported,
and general-purpose codecs that we recommend for widespread usage in new developments.

Also, it's just plain nice to have something in-repo for development purposes.
It makes sure that if we try to make any API changes, we immediately see if they'd make codecs harder to implement.
We also use the batteries-included codecs for debugging, for test fixtures, and for benchmarking.

Further yet, the batteries-included codecs let us offer getting-started APIs.
For example, we offer some helper APIs which use codecs like e.g. JSON to give consumers of the libraries
one-step helper methods that "do the right thing" with zero config... so long as they happen to use that codec.
Even for consumers who don't use those codecs, such functions then serve as natural documentation
and examples for what to do to put their codec of choice to work.
