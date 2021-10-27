hackme
======

Design rational are documented here.

This doc is not necessary reading for users of this package,
but if you're considering submitting patches -- or just trying to understand
why it was written this way, and check for reasoning that might be dated --
then it might be useful reading.

It may also be an incomplete doc.  It's been written opportunistically.
If you don't understand the rationale for some things, try checking git history
(many of the commit messages are downright bookish), or get in touch via
a github issue, irc, matrix, etc and ask!


about NodeAssembler and NodeBuilder
-----------------------------------

See the godoc on these types.

In short, a `NodeBuilder` is for creating a new piece of memory;
a `NodeAssembler` is for instantiating some memory which you already have.

Generally, you'll start any function using a `NodeBuilder`, but then continue
and recurse by passing on the `NodeAssembler`.

See the `./HACKME_builderBehaviors.md` doc for more details on
high level rules and implementation patterns to look out for.



about NodePrototype
---------------

### NodePrototype promises information without allocations

You'll notice nearly every `ipld.NodePrototype` implementation is
a golang struct type with _zero fields_.

This is important.
Getting a NodePrototype is generally expected to be "free" (i.e., zero allocations),
while `NewBuilder` is allowed to be costly (usually causes at least one allocation).
Zero-member structs can be referred to by an interface without requiring an allocation,
which is how it's possible ensure `NodePrototype` are always "free" to refer to.

(Note that a `NodePrototype` that bundles some information like ADL configuration
will subvert this pattern -- but these are an exception, not the rule.)

### NodePrototype reported by a Node

`ipld.NodePrototype` is a type that opaquely represents some information about how
a node was constructed and is implemented.  The general contract for what
should happen when asking a node about its prototype
(via the `ipld.Node.Prototype() NodePrototype` interface) is that prototype should contain
effective instructions for how one could build a copy of that node, using
the same implementation details.

By example, if some node `n` was made as a `basicnode.plainString`,
then `n.Prototype()` will be `basicnode.Prototype__String{}`,
and `n.Prototype().NewBuilder().AssignString("xyz")` can be presumed to work.

Note there are also limits to this: if a node was built in a flexible way,
the prototype it reports later may only report what it is now, and not return
that same flexibility again.
By example, if something was made as an "any" -- i.e.,
via `basicnode.Prototype__Any{}.NewBuilder()`, and then *happened* to be assigned a string value --
the resulting node will still carry a `Prototype()` property that returns
`Prototype__String` -- **not** `Prototype__Any`.

#### NodePrototype meets generic transformation

One of the core purposes of the `NodePrototype` interface (and all the different
ways you can get it from existing data) is to enable the `traversal` package
(or other user-written packages like it) to do transformations on data.

// work-in-progress warning: generic transformations are not fully implemented.

When implementating a transformation that works over unknown data,
the signiture of function a user provides is roughly:
`func(oldValue Node, acceptableValues NodePrototype) (Node, error)`.
(This signiture may vary by the strategy taken by the transformation -- this
signiture is useful because it's capable of no-op'ing; an alternative signiture
might give the user a `NodeAssembler` instead of the `NodePrototype`.)

In this situation, the transformation system determines the `NodePrototype`
(or `NodeAssembler`) to use by asking the parent value of the one we're visiting.
This is because we want to give the update function the ability to create
any kind of value that would be accepted in this position -- not just create a
value of the same prototype as the one currently there!  It is for this reason
the `oldValue.Prototype()` property can't be used directly.

At the root of such a transformation, we use the `node.Prototype()` property to
determine how to get started building a new value.

#### NodePrototype meets recursive assemblers

Asking for a NodePrototype in a recursive assembly process tells you about what
kind of node would be accepted in an `AssignNode(Node)` call.
It does *not* make any remark on the fact it's a key assembler or value assembler
and might be wrapped with additional rules (such as map key uniqueness, field
name expectations, etc).

(Note that it's also not an exclusive statement about what `AssignNode(Node)` will
accept; e.g. in many situations, while a `Prototype__MyStringType` might be the prototype
returned, any string kinded node can be used in `AssignNode(Node)` and will be
appropriately converted.)

Any of these paths counts as "recursive assembly process":

- `MapAssembler.KeyPrototype()`
- `MapAssembler.ValuePrototype(string)`
- `MapAssembler.AssembleKey().Prototype()`
- `MapAssembler.AssembleValue().Prototype()`
- `ListAssembler.ValuePrototype()`
- `ListAssembler.AssembleValue().Prototype()`

### NodePrototype for carrying ADL configuration

// work-in-progress warning: this is an intention of the design, but not implemented.
