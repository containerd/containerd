go-ipld-prime
=============

`go-ipld-prime` is an implementation of the IPLD spec interfaces,
a batteries-included codec implementations of IPLD for CBOR and JSON,
and tooling for basic operations on IPLD objects (traversals, etc).



API
---

The API is split into several packages based on responsibly of the code.
The most central interfaces are the base package,
but you'll certainly need to import additional packages to get concrete implementations into action.

Roughly speaking, the core package interfaces are all about the IPLD Data Model;
the `codec/*` packages contain functions for parsing serial data into the IPLD Data Model,
and converting Data Model content back into serial formats;
the `traversal` package is an example of higher-order functions on the Data Model;
concrete `ipld.Node` implementations ready to use can be found in packages in the `node/*` directory;
and several additional packages contain advanced features such as IPLD Schemas.

(Because the codecs, as well as higher-order features like traversals, are
implemented in a separate package from the core interfaces or any of the Node implementations,
you can be sure they're not doing any funky "magic" -- all this stuff will work the same
if you want to write your own extensions, whether for new Node implementations
or new codecs, or new higher-order order functions!)

- `github.com/ipld/go-ipld-prime` -- imported as just `ipld` -- contains the core interfaces for IPLD.  The most important interfaces are `Node`, `NodeBuilder`, `Path`, and `Link`.
- `github.com/ipld/go-ipld-prime/node/basic` -- imported as `basicnode` -- provides concrete implementations of `Node` and `NodeBuilder` which work for any kind of data.
- `github.com/ipld/go-ipld-prime/traversal` -- contains higher-order functions for traversing graphs of data easily.
- `github.com/ipld/go-ipld-prime/traversal/selector` -- contains selectors, which are sort of like regexps, but for trees and graphs of IPLD data!
- `github.com/ipld/go-ipld-prime/codec` -- parent package of all the codec implementations!
- `github.com/ipld/go-ipld-prime/codec/dagcbor` -- implementations of marshalling and unmarshalling as CBOR (a fast, binary serialization format).
- `github.com/ipld/go-ipld-prime/codec/dagjson` -- implementations of marshalling and unmarshalling as JSON (a popular human readable format).
- `github.com/ipld/go-ipld-prime/linking/cid` -- imported as `cidlink` -- provides concrete implementations of `Link` as a CID.  Also, the multicodec registry.
- `github.com/ipld/go-ipld-prime/schema` -- contains the `schema.Type` and `schema.TypedNode` interface declarations, which represent IPLD Schema type information.
- `github.com/ipld/go-ipld-prime/node/typed` -- provides concrete implementations of `schema.TypedNode` which decorate a basic `Node` at runtime to have additional features described by IPLD Schemas.



Other IPLD Libraries
--------------------

The IPLD specifications are designed to be language-agnostic.
Many implementations exist in a variety of languages.

For overall behaviors and specifications, refer to the IPLD website, or its source, in IPLD meta repo:
- https://ipld.io/
- https://github.com/ipld/ipld/
You should find specs in the `specs/` dir there,
human-friendly docs in the `docs/` dir,
and information about _why_ things are designed the way they are mostly in the `design/` directories.


### distinctions from go-ipld-interface&go-ipld-cbor

This library ("go ipld prime") is the current head of development for golang IPLD,
and we recommend new developments in golang be done using this library as the basis.

However, several other libraries exist in golang for working with IPLD data.
Most of these predate go-ipld-prime and no longer receive active development,
but since they do support a lot of other software, you may continue to seem them around for a while.
go-ipld-prime is generally **serially compatible** with these -- just like it is with IPLD libraries in other languages.

In terms of programmatic API and features, go-ipld-prime is a clean take on the IPLD interfaces,
and chose to address several design decisions very differently than older generation of libraries:

- **The Node interfaces map cleanly to the IPLD Data Model**;
- Many features known to be legacy are dropped;
- The Link implementations are purely CIDs (no "name" nor "size" properties);
- The Path implementations are provided in the same box;
- The JSON and CBOR implementations are provided in the same box;
- Several odd dependencies on blockstore and other interfaces that were closely coupled with IPFS are replaced by simpler, less-coupled interfaces;
- New features like IPLD Selectors are only available from go-ipld-prime;
- New features like ADLs (Advanced Data Layouts), which provide features like transparent sharding and indexing for large data, are only available from go-ipld-prime;
- Declarative transformations can be applied to IPLD data (defined in terms of the IPLD Data Model) using go-ipld-prime;
- and many other small refinements.

In particular, the clean and direct mapping of "Node" to concepts in the IPLD Data Model
ensures a much more consistent set of rules when working with go-ipld-prime data, regardless of which codecs are involved.
(Codec-specific embellishments and edge-cases were common in the previous generation of libraries.)
This clarity is also what provides the basis for features like Selectors, ADLs, and operations such as declarative transformations.

Many of these changes had been discussed for the other IPLD codebases as well,
but we chose clean break v2 as a more viable project-management path.
Both go-ipld-prime and these legacy libraries can co-exist on the same import path, and both refer to the same kinds of serial data.
Projects wishing to migrate can do so smoothly and at their leisure.

We now consider many of the earlier golang IPLD libraries to be defacto deprecated,
and you should expect new features *here*, rather than in those libraries.
(Those libraries still won't be going away anytime soon, but we really don't recomend new construction on them.)

### unixfsv1

Be advised that faculties for dealing with unixfsv1 data are still limited.
You can find some tools for dealing with dag-pb (the underlying codec) in the [ipld/go-codec-dagpb](https://github.com/ipld/go-codec-dagpb) repo,
and there are also some tools retrofitting some of unixfsv1's other features to be perceivable using an ADL in the [ipfs/go-unixfsnode](https://github.com/ipfs/go-unixfsnode) repo...
however, a "some assembly required" advisory may still be in effect; check the readmes in those repos for details on what they support.



Change Policy
-------------

The go-ipld-prime library is already usable.  We are also still in development, and may still change things.

A changelog can be found at [CHANGELOG.md](CHANGELOG.md).

Using a commit hash to pin versions precisely when depending on this library is advisable (as it is with any other).

We may sometimes tag releases, but it's just as acceptable to track commits on master without the indirection.

The following are all norms you can expect of changes to this codebase:

- The `master` branch will not be force-pushed.
    - (exceptional circumstances may exist, but such exceptions will only be considered valid for about as long after push as the "$N-second-rule" about dropped food).
    - Therefore, commit hashes on master are gold to link against.
- All other branches *will* be force-pushed.
    - Therefore, commit hashes not reachable from the master branch are inadvisable to link against.
- If it's on master, it's understood to be good, in as much as we can tell.
- Development proceeds -- both starting from and ending on -- the `master` branch.
    - There are no other long-running supported-but-not-master branches.
    - The existence of tags at any particular commit do not indicate that we will consider starting a long running and supported diverged branch from that point, nor start doing backports, etc.
- All changes are presumed breaking until proven otherwise; and we don't have the time and attention budget at this point for doing the "proven otherwise".
    - All consumers updating their libraries should run their own compiler, linking, and test suites before assuming the update applies cleanly -- as is good practice regardless.
    - Any idea of semver indicating more or less breakage should be treated as a street vendor selling potions of levitation -- it's likely best disregarded.

None of this is to say we'll go breaking things willy-nilly for fun; but it *is* to say:

- Staying close to master is always better than not staying close to master;
- and trust your compiler and your tests rather than tea-leaf patterns in a tag string.


### Version Names

When a tag is made, version number steps in go-ipld-prime advance as follows:

1. the number bumps when the lead maintainer says it does.
2. even numbers should be easy upgrades; odd numbers may change things.
3. the version will start with `v0.` until further notice.

[This is WarpVer](https://gist.github.com/warpfork/98d2f4060c68a565e8ad18ea4814c25f).

These version numbers are provided as hints about what to expect,
but ultimately, you should always invoke your compiler and your tests to tell you about compatibility.


### Updating

**Read the [CHANGELOG](CHANGELOG.md).**

Really, read it.  We put exact migration instructions in there, as much as possible.  Even outright scripts, when feasible.

An even-number release tag is usually made very shortly before an odd number tag,
so if you're cautious about absorbing changes, you should update to the even number first,
run all your tests, and *then* upgrade to the odd number.
Usually the step to the even number should go off without a hitch, but if you *do* get problems from advancing to an even number tag,
A) you can be pretty sure it's a bug, and B) you didn't have to edit a bunch of code before finding that out.
