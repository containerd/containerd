CHANGELOG
=========

Here is collected some brief notes on major changes over time, sorted by tag in which they are first available.

Of course for the "detailed changelog", you can always check the commit log!  But hopefully this summary _helps_.

Note about version numbering: All release tags are in the "v0.${x}" range.  _We do not expect to make a v1 release._
Nonetheless, this should not be taken as a statement that the library isn't _usable_ already.
Much of this code is used in other libraries and products, and we do take some care about making changes.
(If you're ever wondering about stability of a feature, ask -- or contribute more tests ;))

- [Planned/Upcoming Changes](#planned-upcoming-changes)
- [Changes on master branch but not yet Released](#unreleased-on-master)
- [Released Changes Log](#released-changes)


Planned/Upcoming Changes
------------------------

Here are some outlines of changes we intend to make that affect the public API:

- ... the critical list is empty, for now :)

This is not an exhaustive list of planned changes, and does not include any internal changes, new features, performance improvements, and so forth.
It's purely a list of things you might want to know about as a downstream consumer planning your update cycles.

We will make these changes "soon" (for some definition of "soon").
They are currently not written on the master branch.
The definition of "soon" may vary, in service of a goal to sequence any public API changes in a way that's smooth to migrate over, and make those changes appear at an overall bearable chronological frequency.
Tagged releases will be made when any of these changes land, so you can upgrade intentionally.


Unreleased on master
--------------------

Changes here are on the master branch, but not in any tagged release yet.
When a release tag is made, this block of bullet points will just slide down to the [Released Changes](#released-changes) section.

- _nothing yet :)_



Released Changes
----------------

### v0.11.0

_2021 August 12_

This release is an odd numbered release, which means it may contain breaking changes.

Unfortunately, the changes here may be particularly, tricky, as well -- for the most part, they're not compile-time detectable.
They're behavioral changes.  Much more subtle.  Run tests on your systems before accepting these changes.
Specifically: several codecs now enforce sorting when emitting serial data.

There's also some details of what's changing that makes it milder than it first sounds:
most of the changes are around codecs becoming *more* spec-compliant.
So, for example, if you were using another IPLD library that always enforced sorting on e.g. DAG-CBOR,
you won't be surprised or experience it much like a "change" when using this version of go-ipld-prime, which now also enforces such sorting in that codec.

See the full change list for details:

- New: some new helpful constructors for making Selectors out of serial forms can now be found in the `traversal/selector/parse` package.
  [[#199](https://github.com/ipld/go-ipld-prime/pull/199)]
	- Some constants are also included which show some examples of creating common selectors from JSON.
- Fixed: cbor, dag-cbor, json, and dag-json codecs now all accept parsing a block that contains just a null token alone.  (Previously, this returned an "unexpected EOF" error, which was silly.)
  [[#217](https://github.com/ipld/go-ipld-prime/pull/217)]
- Fixed (upstream): json floats are actually supported.  (You might've had this already, if anything dragged in a newer version of the `refmt` library.  We just make sure to require this ourselves in our `go.mod` file now.)
  [[#215](https://github.com/ipld/go-ipld-prime/pull/215)]
- New: Selectors now support some kinds of conditions.  Specifically, `ExploreRecursive` clauses can contain a `stopAt` condition, and the condition system now supports `Condition_IsLink`, which can be used to do an equality check for CIDs.
  [[#214](https://github.com/ipld/go-ipld-prime/pull/214)]
- Fixed: in codegen'd types, the `LinkTargetNodePrototype` on links was returning the wrong prototype; now it returns the right one.
  [[#211](https://github.com/ipld/go-ipld-prime/pull/211)]
- New: `schema.TypedPrototype` interface, which is like `ipld.NodePrototype` but also has methods for asking `Type() schema.Type` and `Representation() ipld.NodePrototype`, both of which should probably instantly make sense to you.
  [[#195](https://github.com/ipld/go-ipld-prime/pull/195)]
- Changed: the dag-json and dag-cbor codecs now apply sorting.
  [[#203](https://github.com/ipld/go-ipld-prime/pull/203), [#204](https://github.com/ipld/go-ipld-prime/pull/204)]
	- This means all serial data created with these codecs is sorted as advised by their respective specifications.
	  Previously, the implementations of these codecs was order-preserving, and emitted data in whatever order the `ipld.Node` yielded it.
	- There may be new performance costs originating from this sorting.
	- The codecs do not reject other orderings when parsing serial data.
	  The `ipld.Node` trees resulting from deserialization will still preserve the serialized order.
	  However, it has now become impossible to re-encode data in that same preserved order.
	- If doing your own encoding, there are customization options in `dagcbor.EncodeOptions.MapSortMode` and `dagjson.EncodeOptions.MapSortMode`.
	  (However, note that these options are not available to you while using any systems that only operate in terms of multicodec codes.)
	- _Be cautious of this change._  It is now extremely easy to write code which puts data into an `ipld.Node` in memory in one order,
	  then save and load that data using these codecs, and end up with different data as a result because the sorting changes the order of data.
	  For some applications, this may not be a problem; for others, it may be surprising.
	  In particular, mind this carefully in the presense of other order-sensitive logic -- for example,
	  such as when using Selectors, whose behaviors also depend on ordering of data returned when iterating over an `ipld.Node`.
- Fixed/Changed: the dag-json codec no longer emits whitespace (!).  It is now spec-compliant.
  [[#202](https://github.com/ipld/go-ipld-prime/pull/202)]
	- This means hashes of content produced by dag-json codec will change.  This is unfortunate, but the previous implementation was woefully and wildly out of sync with the spec, and addressing that is a predominating concern.
- Removed: `fluent/quip` has been dropped.  `fluent/qp` is superior.  `fluent/quip` was too easy to use incorrectly, so we no longer offer it.
  [[#197](https://github.com/ipld/go-ipld-prime/pull/197)]
	- This was an experimental package introduced a few releases ago, together with caveats that we may choose to drop it.  The warning was purposeful!
	  We don't believe that this will be too painful of a change; not many things depended on the `fluent/quip` variant, and those that did should not be difficult to rewrite to `fluent/qp`.
- New: `node/basic.Chooser` is a function that implements `traversal.LinkTargetNodePrototypeChooser`.  It's a small handy quality-of-life increase if you need to supply such a function, which is common.
  [[#198](https://github.com/ipld/go-ipld-prime/pull/198)]
- New: `bindnode`!  **This is a huge feature.**  The beginnings of it may have been visible in v0.10.0, but it's grown into a usable thing we're ready to talk about.
	- Bindnode lets you write golang types and structures, and "bind" them into being IPLD Nodes and supporting Data Model operations by using golang reflection.
	- The result of working with `bindnode` is somewhere between using basicnode and using codegen:
	  it's going to provide some structural constraints (like codegen) and provide moderate performance (it lets you use structs rather than memory-expensive maps; but reflection is still going to be slower than codegen).
	- However, most importantly, `bindnode` is *nice to use*.  It doesn't have a huge barrier to entry like codegen does.
	- `bindnode` can be used with _or without_ IPLD Schemas.  For basic golang types, a schema can be inferred automatically.  For more advanced features (e.g. any representation customization), you can provide a Schema.
	- Please note that though it is now usable, bindnode remains _in development_.  There is not yet any promise that it will be frozen against changes.
		- In fact, several changes are expected; in particular, be advised there is some sizable change expected around the shape of golang types expected for unions.
- Improved: tests for behavior of schema typed nodes are now extracted to a package, where they are reusable.
	- The same tests now cover the `bindnode` implementation, as well as being used in tests of our codegen outputs.
	- Previously, these tests were already mostly agnostic of implementation, but had been thrown into packages in a way that made them hard to reuse.
- Improved (or Fixed, depending on your point of view): dag-json codec now supports bytes as per the spec.
  [[#166](https://github.com/ipld/go-ipld-prime/pull/166),[#216](https://github.com/ipld/go-ipld-prime/pull/216)]
	- Bytes are encoded in roughly this form: `{"/":{"bytes":"base64data"}}`.
	- Note: the json codec does _not_ include this behavior; this is behavior specific to dag-json.



### v0.10.0

_2021 June 02_

v0.10.0 is a mild release, containing _no_ breaking changes, but lots of cool new stuff.  Update at your earliest convenience.

There's a bunch of cool new features in here, some of which are significant power-ups for the ecosystem (e.g. the `NodeReifier` API), so we recommend updating as soon as possible.

There's also some sizable performance improvements available for generated code, so go forth and update your generated code too!

Check out the full feature list:

- New: an `ipld.DeepEqual` method lets you easily compare two `ipld.Node` for equality.  (This is useful in case you have nodes with two different internal implementations, different memory layouts, etc, such that native golang equality would not be semantically correct.)
  [[#174](https://github.com/ipld/go-ipld-prime/pull/174)]
- New: the multicodec package exposes a `multicodec.Registry` type, and also some `multicodec.List*` methods.
  [[#172](https://github.com/ipld/go-ipld-prime/pull/172), [#176](https://github.com/ipld/go-ipld-prime/pull/176)]
	- Please be cautious of using these `List*` methods.  It's very possible to create race conditions with these, especially if using them on the global default registry instance.
	  If we detect that these access methods seem to produce a source of bugs and design errors in downstream usage, they will be removed.
	  Consider doing whatever you're doing by buildling your own registry systems, and attaching whatever semantics your system desires to those systems, rather than shoehorning this intentionally limited system into doing things it isn't made to do.
- Improved: the dag-json codec now actually supports bytes!
  (Perhaps surprisingly, this was a relatively recent addition to the dag-json spec.  We've now caught up with it.)
  [[#166](https://github.com/ipld/go-ipld-prime/pull/166)]
- Improved: the codegen system now gofmt's the generated code immediately.  You no longer need to do this manually in a separate step.
  [[#163](https://github.com/ipld/go-ipld-prime/pull/163)]
- Improved: the codegen system is slightly faster at emitting code (due to use of more buffering during writes).
  [[#161](https://github.com/ipld/go-ipld-prime/pull/161)]
- Improved: the codegen system will now avoid pointers in the generated "Maybe" types, if they're known to be small in memory (and thus, reasonable to inline).
  [[#160](https://github.com/ipld/go-ipld-prime/pull/160)]
	- This is quite likely to result in performance improvements for most programs, as it decreases the number of small memory allocations done, and amount of time spent on dereferencing, cache misses, etc.
	  Some workloads demonstrated over 10% speed increases, and 40% decreases in allocation counts.
	  (Of course, run your own benchmarks; not all workloads are equal.)
- New: `ipld.LinkSystem` now contains a "reification" hook system.  **This is really cool.**
	- The center of this is the `ipld.LinkSystem.NodeReifier` field, and the `ipld.NodeReifier` function type.
	- The `ipld.NodeReifier` function type is simply `func(LinkContext, Node, *LinkSystem) (Node, error)`.
	- The purpose and intention of this is: you can use this hooking point in order to decide where to engage advanced IPLD features like [ADLs](https://ipld.io/glossary/#adl).
	  One can use a `NodeReifier` to decide what ADLs to use and when... even when in the middle of a traversal.
	- For example: one could write a NodeReifier that says "when I'm in a path that ends with '`foosys/*/hamt`', i'm going to try to load that as if it's a HAMT ADL".
	  With that hook in place, you'd then be able to walks over whole forests of data with `traversal.*` functions, and they would automatically load the relevant ADL for you transparently every time that pattern is encountered, without disrupting or complicating the walk.
	- In the future, we might begin to offer more structural and declaratively configurable approaches to this, and eventually, attempt to standardize them.
	  For now: you can build any solution you like using this hook system.  (And we'll probably plug in any future declarative systems via these same hooks, too.)
	- All this appeared in [#158](https://github.com/ipld/go-ipld-prime/pull/158).
- New: `ipld.LinkSystem` now contains a boolean flag for `TrustedStorage`.  If set to true, it will cause methods like `Load` to _skip hashing_ when loading content.  **_Do not do this unless you know what you're doing._**
  [[#149](https://github.com/ipld/go-ipld-prime/pull/149)]
- New: a json (as opposed to dag-json) codec is now available from this repo.  It does roughly what you'd expect.  (It's like dag-json, but explicitly rejects encoding links and bytes, and correspondingly does not have dag-json's special decoding behaviors that produce those kinds.)
  [[#152](https://github.com/ipld/go-ipld-prime/pull/152)]
- New: a cbor (as opposed to dag-cbor) codec is now available from this repo.  Same story as the json codec: it just explicitly doesn't support links (because you should use dag-cbor if you want that).
  [[#153](https://github.com/ipld/go-ipld-prime/pull/153)]

This contained a ton of contributions from lots of people: especially thanks to @mvdan, @hannahhoward, and @willscott for invaluable contributions.



### v0.9.0

_2021 March 15_

v0.9.0 is a pretty significant release, including several neat new convenience features, but most noticeably, significantly reworking how linking works.

Almost any code that deals with storing and linking data will need some adaptation to handle this release.
We're sorry about the effort this may require, but it should be worth it.
The new LinkSystem API should let us introduce a lot more convenience features in the future, and do so *without* pushing additional breakage out to downstream users; this is an investment in the future.

The bullet points below contain all the fun details.

Note that a v0.8.0 release version has been skipped.
We use odd numbers to indicate the existence of significant changes;
and while usually we try to tag an even-number release between each odd number release so that migrations can be smoothed out,
in this case there simply weren't enough interesting points in between to be worth doing so.

- Change: linking has been significantly reworked, and now primarily works through the `ipld.LinkSystem` type.
	- This is cool, because it makes a lot of things less circuitous.  Previously, working with links was a complicated combination of Loader and Storer functions, the Link interface contained the Load method, it was just... complicated to figure out where to start.  Now, the answer is simple and constant: "Start with LinkSystem".  Clearer to use; clearer to document; and also coincidentally a lot clearer to develop for, internally.
	- The PR can be found here: https://github.com/ipld/go-ipld-prime/pull/143
	- `Link.Load` -> `LinkSystem.Load` (or, new: `LinkSystem.Fill`, which lets you control memory allocation more explicitly).
	- `LinkBuilder.Build` -> `LinkSystem.Store`.
	- `LinkSystem.ComputeLink` is a new feature that produces a Link without needing to store the data anywhere.
	- The `ipld.Loader` function is now most analogous to `ipld.BlockReadOpener`.  You now put it into use by assigning it to a `LinkLoader`'s `StorageReadOpener` field.
	- The `ipld.Storer` function is now most analogous to `ipld.BlockWriteOpener`.  You now put it into use by assigning it to a `LinkLoader`'s `StorageWriteOpener` field.
	- 99% of the time, you'll probably start with `linking/cid.DefaultLinkSystem()`.  You can assign to fields of this to customize it further, but it'll get you started with multihashes and multicodecs and all the behavior you expect when working with CIDs.
		- (So, no -- the `cidlink` package hasn't gone anywhere.  Hopefully it's a bit less obtrusive now, but it's still here.)
	- The `traversal` package's `Config` struct now uses a `LinkSystem` instead of a `Loader` and `Storer` pair, as you would now probably expect.
		- If you had code that was also previously passing around `Loader` and `Storer`, it's likely a similar pattern of change will be the right direction for that code.
	- In the _future_, further improvements will come from this: we're now much, much closer to making a bunch of transitive dependencies become optional (especially, various hashers, which currently, whenever you pull in the `linking/cid` package, come due to `go-cid`, and are quite large).  When these improvements land (again, they're not in this release), you'll need to update your applications to import hashers you need if they're not in the golang standard library.  For now: there's no change.
- Change: multicodec registration is now in the `go-ipld-prime/multicodec` package.
	- Previously, this registry was in the `linking/cid` package.  These things are now better decoupled.
	- This will require packages which register codecs to make some very small updates: e.g. `s/cidlink.RegisterMulticodecDecoder/multicodec.RegisterDecoder/`, and correspondingly, update the package imports at the top of the file.
- New: some pre-made storage options (e.g. satisfying the `ipld.StorageReadOpener` and `ipld.StorageWriteOpener` function interfaces) have appeared!  Find these in the `go-ipld-prime/storage` package.
	- Currently this only includes a simple in-memory storage option.  This may be useful for testing and examples, but probably not much else :)
	- These are mostly intended to be illustrative.  You should still expect to find better storage mechanisms in other repos.
- Change: some function names in codec packages are ever-so-slightly updated.  (They're verbs now, instead of nouns, which makes sense because they're functions.  I have no idea what I was thinking with the previous naming.  Sorry.)
	- `s/dagjson.Decoder/dagjson.Decode/g`
	- `s/dagjson.Decoder/dagjson.Encode/g`
	- `s/dagcbor.Decoder/dagcbor.Decode/g`
	- `s/dagcbor.Encoder/dagcbor.Encode/g`
	- If you've only been using these indirectly, via their multicodec indicators, you won't have to update anything at all to account for this change.
- New: several new forms of helpers to make it syntactically easy to create new IPLD data trees with golang code!
	- Check out the `go-ipld-prime/fluent/quip` package!  See https://github.com/ipld/go-ipld-prime/pull/134, where it was introduced, for more details.
	- Check out the `go-ipld-prime/fluent/qp` package!  See https://github.com/ipld/go-ipld-prime/pull/138, where it was introduced, for more details.
	- Both of these offer variations on `fluent` which have much lower costs to use.  (`fluent` incurs allocations during operation, which has a noticable impact on performance if used in a "hot" code path.  Neither of these two new solutions do!)
	- For now, both `quip` and `qp` will be maintained.  They have similar goals, but different syntaxes.  If one is shown drastically more popular over time, we might begin to consider deprecating one in favor of the other, but we'll need lots of data before considering that.
	- We won't be removing the `fluent` package anytime soon, but we probably wouldn't recommend building new stuff on it.  `qp` and `quip` are both drastically preferable for performance reasons.
- New: there is now an interface called `ipld.ADL` which can be used for a certain kind of feature detection.
	- This is an experimental new concept and likely subject to change.
	- The one key trait we've found all ADLs tend to share right now is that they have a "synthesized" view and "substrate" view of their data.  So: the `ipld.ADL` interface states that a thing is an `ipld.Node` (for the synthesized view), and from it you should be able to access a `Substrate() ipld.Node`, and that's about it.



### v0.7.0

_2020 December 31_

v0.7.0 is a small release that makes a couple of breaking changes since v0.6.0.
However, the good news is: they're all very small changes, and we've kept them in a tiny group,
so if you're already on v0.6.0, this update should be easy.
And we've got scripts to help you.

There's also one cool new feature: `traversal.FocusedTransform` is now available to help you make mutations to large documents conveniently.

- Change: all interfaces and APIs now use golang `int64` rather than golang `int`.  [#125](https://github.com/ipld/go-ipld-prime/pull/125)
	- This is necessary because the IPLD Data Model specifies that integers must be "at least 2^53" in range, and so since go-ipld-prime may also be used on 32-bit architectures, it is necessary that we not use the `int` type, which would fail to be Data Model-compliant on those architectures.
	- The following GNU sed lines should assist this transition in your code, although some other changes that are more difficult automate may also be necessary:
		```
		sed -ri 's/(func.* AsInt.*)\<int\>/\1int64/g' **/*.go
		sed -ri 's/(func.* AssignInt.*)\<int\>/\1int64/g' **/*.go
		sed -ri 's/(func.* Length.*)\<int\>/\1int64/g' **/*.go
		sed -ri 's/(func.* LookupByIndex.*)\<int\>/\1int64/g' **/*.go
		sed -ri 's/(func.* Next.*)\<int\>/\1int64/g' **/*.go
		sed -ri 's/(func.* ValuePrototype.*)\<int\>/\1int64/g' **/*.go
		```
- Change: we've renamed the types talking about "kinds" for greater clarity.  `ipld.ReprKind` is now just `ipld.Kind`; `schema.Kind` is now `schema.TypeKind`.  We expect to use "kind" and "typekind" consistently in prose and documentation from now on, as well.  [#127](https://github.com/ipld/go-ipld-prime/pull/127)
	- Pretty much everyone who's used this library has said "ReprKind" didn't really make sense as a type name, so, uh, yeah.  You were all correct.  It's fixed now.
	- "kind" now always means "IPLD Data Model kind", and "typekind" now always means "the kinds which an IPLD Schema type can have".
	- You can find more examples of how we expect to use this in a sentence from now on in the discussion that lead to the rename: https://github.com/ipld/go-ipld-prime/issues/94#issuecomment-745307919
	- The following GNU sed lines should assist this transition in your code:
		```
		sed -ri 's/\<Kind\(\)/TypeKind()/g' **/*.go
		sed -ri 's/\<Kind_/TypeKind_/g' **/*.go
		sed -i 's/\<Kind\>/TypeKind/g' **/*.go
		sed -i 's/ReprKind/Kind/g' **/*.go
		```
- Feature: `traversal.FocusedTransform` works now!  :tada:  You can use this to take a node, say what path inside it you want to update, and then give it an updated value.  Super handy.  [#130](https://github.com/ipld/go-ipld-prime/pull/130)


### v0.6.0

_2020 December 14_

v0.6.0 is a feature-packed release and has a few bugfixes, and _no_ significant breaking changes.  Update at your earliest convenience.

Most of the features have to do with codegen, which we now consider to be in **alpha** -- go ahead and use it!  (We're starting to self-host some things in it, so any changes will definitely be managed from here on out.)
A few other small handy helper APIs have appeared as well; see the detailed notes for those.

Like with the last couple of releases, our intent is to follow this smooth-sailing change with another release shortly which will include some minor but noticable API changes, and that release may require you to make some code changes.
Therefore, we suggest upgrading to this one first, beacuse it's an easy waypoint before the next change.

- Feature: codegen is a reasonably usable alpha!  We now encourage trying it out (but still only for those willing to experience an "alpha" level of friction -- UX still rough, and we know it).
	- Consult the feature table in the codegen package readme: many major features of IPLD Schemas are now supported.
		- Structs with tuple representations?  Yes.
		- Keyed unions?  Yes.
		- Structs with stringjoin representations?  Yes.  Including nested?  _Yes_.
		- Lots of powerful stuff is now available to use.
		- See [the feature table in the codegen readme](https://github.com/ipld/go-ipld-prime/blob/v0.6.0/schema/gen/go/README.md#completeness) for details.
	- Many generated types now have more methods for accessing them in typed ways (in addition to the usual `ipld.Node` interfaces, which can access the same data, but lose explicit typing).  [#106](https://github.com/ipld/go-ipld-prime/pull/106)
		- Maps and lists now have both lookup methods and iterators which know the type of the child keys and values explicitly.
	- Cool: when generating unions, you can choose between different implementation strategies (favoring either interfaces, or embedded values) by using Adjunct Config.  This lets you tune for either speed (reduced allocation count) or memory footprint (less allocation size, but more granular allocations).
		- See notes in [#60](https://github.com/ipld/go-ipld-prime/pull/60) for more detail on this.  We'll be aiming to make configurability of this more approachable and better documented in future releases, as we move towards codegen tools usable as CLI tools.
	- Cyclic references in types are now supported.
		- ... mostly.  Some manual configuration may sometimes be required to make sure the generated structure wouldn't have an infinite memory size.  We'll keep working on making this smoother in the future.
	- Field symbol overrides now work properly.  (E.g., if you have a schema with a field called "type", you can make that work now.  Just needs a field symbol override in the Adjunct Config when doing codegen!)
	- Codegen'd link types now implemented the `schema.TypedLinkNode` interface where applicable.
	- Structs now actually validate all required fields are present before allowing themselves to finish building.  Ditto for their map representations.
	- Much more testing.  And we've got a nice new declarative testcase system that makes it easier to write descriptions of how data should behave (at both the typed and representation view levels), and then just call one function to run exhaustive tests to make sure it looks the same from every inspectable API.
	- Change: codegen now outputs a fixed set of files.  (Previously, it output one file per type in your schema.)  This makes codegen much more managable; if you remove a type from your schema, you don't have to chase down the orphaned file.  It's also just plain less clutter to look at on the filesystem.
- Demo: as proof of the kind of work that can be done now with codegen, we've implemented the IPLD Schema schema -- the schema that describes IPLD Schema declarations -- using codegen.  It's pretty neat.
	- Future: we'll be replacing most of the current current `schema` package with code based on this generated stuff.  Not there yet, though.  Taking this slow.
		- You can see the drafts of this, along with new features based on it, in [#107](https://github.com/ipld/go-ipld-prime/pull/107).
- Feature: the `schema` typesystem info packages are improved.
	- Cyclic references in types are now supported.
		- (Mind that there are still some caveats about this when fed to codegen, though.)
	- Graph completeness is now validated (e.g. missing type references emit useful errors)!
- Feature: there's a `traversal.Get` function.  It's like `traversal.Focus`, but just returns the reached data instead of dragging you through a callback.  Handy.
- Feature/bugfix: the DAG-CBOR codec now includes resource budgeting limits.  This means it's a lot harder for a badly-formed (or maliciously formed!) message to cause you to run out of memory while processing it.  [#85](https://github.com/ipld/go-ipld-prime/pull/85)
- Bugfix: several other panics from the DAG-CBOR codec on malformed data are now nice politely-returned errors, as they should be.
- Bugfix: in codegen, there was a parity break between the AssembleEntry method and AssembleKey+AssembleValue in generated struct NodeAssemblers.  This has been fixed.
- Minor: ErrNoSuchField now uses PathSegment instead of a string.  You probably won't notice (but this was important interally: we need it so we're able to describe structs with tuple representations).
- Bugfix: an error path during CID creation is no longer incorrectly dropped.  (I don't think anyone ever ran into this; it only handled situations where the CID parameters were in some way invalid.  But anyway, it's fixed now.)
- Performance: when `cidlink.Link.Load` is used, it will do feature detection on its `io.Reader`, and if it looks like an already-in-memory buffer, take shortcuts that do bulk operations.  I've heard this can reduce memory pressure and allocation counts nicely in applications where that's a common scenario.
- Feature: there's now a `fluent.Reflect` convenience method.  Its job is to take some common golang structs like maps and slices of primitives, and flip them into an IPLD Node tree.  [#81](https://github.com/ipld/go-ipld-prime/pull/81)
	- This isn't very high-performance, so we don't really recommend using it in production code (certainly not in any hot paths where performance matters)... but it's dang convenient sometimes.
- Feature: there's now a `traversal.SelectLinks` convenience method.  Its job is to walk a node tree and return a list of all the link nodes.  [#110](https://github.com/ipld/go-ipld-prime/pull/110)
	- This is both convenient, and faster than doing the same thing using general-purpose Selectors (we implemented it as a special case).
- Demo: you can now find a "rot13" ADL in the `adl/rot13adl` package.  This might be useful reference material if you're interested in writing an ADL and wondering what that entails.  [#98](https://github.com/ipld/go-ipld-prime/pull/98)
- In progress: we've started working on some new library features for working with data as streams of "tokens".  You can find some of this in the new `codec/codectools` package.
	- Functions are available for taking a stream of tokens and feeding them into a NodeAssembler; and for taking a Node and reading it out as a stream of tokens.
	- The main goal in mind for this is to provide reusable components to make it easier to implement new codecs.  But maybe there will be other uses for this feature too!
	- These APIs are brand new and are _extremely subject to change_, much more so than any other packages in this repo.  If you work with them at this stage, _do_ expect to need to update your code when things shift.


Released Changes
----------------

### v0.5.0

_2020 July 2_

v0.5.0 is a small release -- it just contains a bunch of renames.
There are _no_ semantic changes bundled with this (it's _just_ renames) so this should be easy to absorb.

- Renamed: `NodeStyle` -> `NodePrototype`.
	- Reason: it seems to fit better!  See https://github.com/ipld/go-ipld-prime/issues/54 for a full discussion.
	- This should be a "sed refactor" -- the change is purely naming, not semantics, so it should be easy to update your code for.
	- This also affects some package-scoped vars named `Style`; they're accordingly also renamed to `Prototype`.
	- This also affects several methods such as `KeyStyle` and `ValueStyle`; they're accordingly also renamed to `KeyPrototype` and `ValuePrototype`.
- Renamed: `(Node).Lookup{Foo}` -> `(Node).LookupBy{Foo}`.
	- Reason: The former phrasing makes it sound like the "{Foo}" component of the name describes what it returns, but in fact what it describes is the type of the param (which is necessary, since Golang lacks function overloading parametric polymorphism).  Adding the preposition should make this less likely to mislead (even though it does make the method name moderately longer).
	- This should be a "sed refactor" -- the change is purely naming, not semantics, so it should be easy to update your code for.
- Renamed: `(Node).Lookup` -> `(Node).LookupNode`.
	- Reason: The shortest and least-qualified name, 'Lookup', should be reserved for the best-typed variant of the method, which is only present on codegenerated types (and not present on the Node interface at all, due to Golang's limited polymorphism).
	- This should be a "sed refactor" -- the change is purely naming, not semantics, so it should be easy to update your code for.  (The change itself in the library was fairly literally `s/Lookup(/LookupNode(/g`, and then `s/"Lookup"/"LookupNode"/g` to catch a few error message strings, so consumers shouldn't have it much harder.)
	- Note: combined with the above rename, this method overall becomes `(Node).LookupByNode`.
- Renamed: `ipld.Undef` -> `ipld.Absent`, and `(Node).IsUndefined` -> `(Node).IsAbsent`.
	- Reason: "absent" has emerged as a much, much better description of what this value means.  "Undefined" sounds nebulous and carries less meaning.  In long-form prose docs written recently, "absent" consistently fits the sentence flow much better.  Let's just adopt "absent" consistently and do away with "undefined".
	- This should be a "sed refactor" -- the change is purely naming, not semantics, so it should be easy to update your code for.


### v0.4.0

v0.4.0 contains some misceleanous features and documentation improvements -- perhaps most notably, codegen is re-introduced and more featureful than previous rounds -- but otherwise isn't too shocking.
This tag mostly exists as a nice stopping point before the next version coming up (which is planned to include several API changes).

- Docs: several new example functions should now appear in the godoc for how to use the linking APIs.
- Feature: codegen is back!  Use it if you dare.
	- Generated code is now up to date with the present versions of the core interfaces (e.g., it's updated for the NodeAssembler world).
	- We've got a nice big feature table in the codegen package readme now!  Consult that to see which features of IPLD Schemas now have codegen support.
	- There are now several implemented and working (and robustly tested) examples of codegen for various representation strategies for the same types.  (For example, struct-with-stringjoin-representation.)  Neat!
	- This edition of codegen uses some neat tricks to not just maintain immutability contracts, but even prevent the creation of zero-value objects which could potentially be used to evade validation phases on objects that have validation rules.  (This is a bit experimental; we'll see how it goes.)
	- There are oodles and oodles of deep documentation of architecture design choices recorded in "HACKME_*" documents in the codegen package that you may enjoy if you want to contribute or understand why generated things are the way they are.
	- Testing infrastructure for codegen is now solid.  Running tests for the codegen package will: exercise the generation itself; AND make sure the generated code compiles; AND run behavioral tests against it: the whole gamut, all from regular `go test`.
	- The "node/gendemo" package contains a real example of codegen output... and it's connected to the same tests and benchmarks as other node implementations.  (Are the gen'd types fast?  yes.  yes they are.)
	- There's still lots more to go: interacting with the codegen system still requires writing code to interact with as a library, as we aren't shipping a CLI frontend to it yet; and many other features are still in development as well.  But you're welcome to take it for a spin if you're eager!
- Feature: introduce JSON Tables Codec ("JST"), in the `codec/jst` package.  This is a codec that emits bog-standard JSON, but leaning in on the non-semantic whitespace to produce aligned output, table-like, for pleasant human reading.  (If you've used `column -t` before in the shell: it's like that.)
	- This package may be a temporary guest in this repo; it will probably migrate to its own repo soon.  (It's a nice exercise of our core interfaces, though, so it incubated here.)
- I'm quietly shifting the versioning up to the 0.x range.  (Honestly, I thought it was already there, heh.)  That makes this this "v0.4".


### v0.0.3

v0.0.3 contained a massive rewrite which pivoted us to using NodeAssembler patterns.
Code predating this version will need significant updates to match; but, the performance improvements that result should be more than worth it.

- Constructing new nodes has a major pivot towards using "NodeAssembler" pattern: https://github.com/ipld/go-ipld-prime/pull/49
	- This was a massively breaking change: it pivoted from bottom-up composition to top-down assembly: allocating large chunks of structured memory up front and filling them in, rather than stitching together trees over fragmented heap memory with lots of pointers
- "NodeStyle" and "NodeBuilder" and "NodeAssembler" are all now separate concepts:
	- NodeStyle is more or less a builder factory (forgive me -- but it's important: you can handle these without causing allocations, and that matters).
	  Use NodeStyle to tell library functions what kind of in-memory representation you want to use for your data.  (Typically `basicnode.Style.Any` will do -- but you have the control to choose others.)
	- NodeBuilder allocates and begins the assembly of a value (or a whole tree of values, which may be allocated all at once).
	- NodeAssembler is the recursive part of assembling a value (NodeBuilder implements NodeAssembler, but everywhere other than the root, you only use the NodeAssembler interface).
- Assembly of trees of values now simply involves asking the assembler for a recursive node to give you assemblers for the keys and/or values, and then simply... using them.
	- This is much simpler (and also faster) to use than the previous system, which involved an awkward dance to ask about what kind the child nodes were, get builders for them, use those builders, then put the result pack in the parent, and so forth.
- Creating new maps and lists now accepts a size hint argument.
	- This isn't strictly enforced (you can provide zero, or even a negative number to indicate "I don't know", and still add data to the assembler), but may improve efficiency by reducing reallocation costs to grow structures if the size can be estimated in advance.
- Expect **sizable** performance improvements in this version, due to these interface changes.
- Some packages were renamed in an effort to improve naming consistency and feel:
	- The default node implementations have moved: expect to replace `impl/free` in your package imports with `node/basic` (which is an all around better name, anyway).
	- The codecs packages have moved: replace `encoding` with `codec` in your package imports (that's all there is to it; nothing else changed).
- Previous demos of code generation are currently broken / disabled / removed in this tag.
	- ...but they'll return in future versions, and you can follow along in branches if you wish.
- Bugfix: dag-cbor codec now correctly handles marshalling when bytes come after a link in the same object. [[53](https://github.com/ipld/go-ipld-prime/pull/53)]

### v0.0.2

- Many various performance improvements, fixes, and docs improvements.
- Many benchmarks and additional tests introduced.
- Includes early demos of parts of the schema system, and early demos of code generation.
- Mostly a checkpoint before beginning v0.0.3, which involved many large API reshapings.

### v0.0.1

- Our very first tag!
- The central `Node` and `NodeBuilder` interfaces are already established, as is `Link`, `Loader`, and so forth.
  You can already build generic data handling using IPLD Data Model concepts with these core interfaces.
- Selectors and traversals are available.
- Codecs for dag-cbor and dag-json are batteries-included in the repo.
- There was quite a lot of work done before we even started tagging releases :)
