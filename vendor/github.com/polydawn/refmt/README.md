refmt [![GoDoc](https://godoc.org/github.com/polydawn/refmt?status.svg)](https://godoc.org/github.com/polydawn/refmt) [![Build status](https://img.shields.io/travis/polydawn/refmt/master.svg?style=flat-square)](https://travis-ci.org/polydawn/refmt)
=====


`refmt` is a serialization and object-mapping library.



Why?
----

Mostly because I have some types which I need to encode in two different ways, and that needs to not suck,
and that totally sucks with most serialization libraries I've used.
Also, I need to serialize things in different formats, e.g. sometimes JSON and other times CBOR,
and that needs to work without me wrestling two different object-serial libraries and configs.

More broadly, I want a single library that can handle my serialization -- with the possibility of different setups on the same types -- and if it can do general object traversals, e.g. a deepcopy, that also seems like... just something that should be natural.

So it seems like there should be some way to define token streams... and a way to define converting objects to and from token streams... and a way to covert token streams to and from serialized bytes... and all of these should be pretty separate!

Thusly was this library thrust into the world:
`refmt/tok` to define the token stream,
and `refmt/obj` to define how to map objects to tokens and back,
and `refmt/json` and `refmt/cbor` as ways to exchange tokens with serialized formats.

All of these formats can mix-n-match freely, because they communicate values as the standard token stream. Voilà:

- pair `obj.Marshaller` with `json.Encoder` to get a json serializer.
- pair `cbor.Decoder` with `obj.Unmarshaller` to get a cbor deserializer.
- pair `cbor.Decoder` with `json.Encoder` to get a cbor->json streaming transcoder!
- pair `obj.Marshaller` with `obj.Unmarshaller` to get a deep-copy system!  (Try it with two different types: marshalling a struct and unmarshalling into a freeform map!)

Along the way, we've added a powerful system for defining **how** exactly the `refmt/obj` tools should treat your structures:
the Atlas system (defined in the `refmt/obj/atlas` package).
Atlases can be used to customize how struct fields are handled, how map keys are sorted, and even
define conversions between completely different *kinds* of objects: serialize arrays as strings, or turn stringy enums into bitfields, no problem.
By default, `refmt` will generate atlases automatically for your structs and types, just like the standard library json marshallers do;
if you want more control, atlases give you the power.

An Atlas can be given to each `obj.Marshaller` and `obj.Unmarshaller` when it is constructed.
This allows great variation in how you wish to handle types -- more than one mapping can be defined for the same concrete type!
(This is a killer feature if you need to support multiple versions of an API, for example:
you can define 'v1' and 'v2' types, each with their own structs to unmarshal user requests into;
then in the backend implement another Marshal/Unmarshal with different atlases which translates the 'v1' requests to 'v2' types,
and you only have to implement business logic against the latest types!)

Atlases are significantly more convenient to use than defining custom `JSONMarshal()` methods.
Atlases attach to the type they concern.
This means you can use atlases to define custom serialization even for types in packages you can't modify!
Atlases also behave better in complex situations: for example,
if you have a `TypeFoo` struct and you wish to serialize it as a string,
*you don't have to write a custom marshaller for every type that **contains** a `TypeFoo` field*.
Leaking details of custom serialization into the types that contain the interesting objects is
a common pitfall when getting into advanced usage of other marshalling libraries; `refmt` has no such issue.

## tl;dr:

- you can swap out atlases for custom serialization on any type;
- and that works for serialization modes even deep in other structures;
- and even on types you don't own!!
- at the same time, you can swap out a cbor encoder for a json encoder, or anything else you want;
- and the mapper part *doesn't care* -- no complex interactions between the layers.

Come to `refmt`.  It's nicer here.


Where do I start?
-----------------

**If you're already using `json.Marshal`:** switch to `json.Marshal` (yes, I'm not kidding; just switch your imports!).

**If you're already using `json.NewEncoder().Encode()`:** switch to `json.NewMarshaller().Marshal()`.

**If you want to get more serial-flexible:** try using `refmt.Marshal(json.EncodeOptions{}, obj)`... then switch to `cbor.EncodeOptions{}` and see what happens!

**If you want to use Atlas to get fancy:** go take a peek at the `example*.go` files in this repo! :)

Happy hacking!
