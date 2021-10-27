hackme
======

Design rationale are documented here.

This doc is not necessary reading for users of this package,
but if you're considering submitting patches -- or just trying to understand
why it was written this way, and check for reasoning that might be dated --
then it might be useful reading.

### scalars are just typedefs

This is noteworthy because in codegen, this is typically *not* the case:
in codegen, even scalar types are boxed in a struct, such that it prevents
casting values into those types.

This casting is not a concern for the node implementations in this package, because

- A) we don't have any kind of validation rules to make such casting worrying; and
- B) since our types are unexported, casting is still blocked by this anyway.

### about builders for scalars

The assembler types for scalars (string, int, etc) are pretty funny-looking.
You might wish to make them work without any state at all!

The reason this doesn't fly is that we have to keep the "wip" value in hand
just long enough to return it from the `NodeBuilder.Build` method -- the
`NodeAssembler` contract for `Assign*` methods doesn't permit just returning
their results immediately.

(Another possible reason is if we expected to use these assemblers on
slab-style allocations (say, `[]plainString`)...
however, this is inapplicable at present, because
A) we don't (except places that have special-case internal paths anyway); and
B) the types aren't exported, so users can't either.)

Does this mean that using `NodeBuilder` for scalars has a completely
unnecessary second allocation, which is laughably inefficient?  Yes.
It's unfortunate the interfaces constrain us to this.
**But**: one typically doesn't actually use builders for scalars much;
they're just here for completeness.
So this is less of a problem in practice than it might at first seem.

More often, one will use the "any" builder (which is has a whole different set
of design constraints and tradeoffs);
or, if one is writing code and knows which scalar they need, the exported
direct constructor function for that kind
(e.g., `String("foo")` instead of `Prototype__String{}.NewBuilder().AssignString("foo")`)
will do the right thing and do it in one allocation (and it's less to type, too).

### maps and list keyAssembler and valueAssemblers have custom scalar handling

Related to the above heading.

Maps and lists in this package do their own internal handling of scalars,
using unexported features inside the package, because they can more efficient.

### when to invalidate the 'w' pointers

The 'w' pointer -- short for 'wip' node pointer -- has an interesting lifecycle.

In a NodeAssembler, the 'w' pointer should be intialized before the assembler is used.
This means either the matching NodeBuilder type does so; or,
if we're inside recursive structure, the parent assembler did so.

The 'w' pointer is used throughout the life of the assembler.

Setting the 'w' pointer to nil is one of two mechanisms used internally
to mark that assembly has become "finished" (the other mechanism is using
an internal state enum field).
Setting the 'w' pointer to nil has two advantages:
one is that it makes it *impossible* to continue to mutate the target node;
the other is that we need no *additional* memory to track this state change.
However, we can't use the strategy of nilling 'w' in all cases: in particular,
when in the NodeBuilder at the root of some construction,
we need to continue to hold onto the node between when it becomes "finished"
and when Build is called; otherwise we can't actually return the value!
Different stratgies are therefore used in different parts of this package.

Maps and lists use an internal state enum, because they already have one,
and so they might as well; there's no additional cost to this.
Since they can use this state to guard against additional mutations after "finish",
the map and list assemblers don't bother to nil their own 'w' at all.

During recursion to assemble values _inside_ maps and lists, it's interesting:
the child assembler wrapper type takes reponsibility for nilling out
the 'w' pointer in the child assembler's state, doing this at the same time as
it updates the parent's state machine to clear proceeding with the next entry.

In the case of scalars at the root of a build, we took a shortcut:
we actually don't fence against repeat mutations at all.
*You can actually use the assign method more than once*.
We can do this without breaking safety contracts because the scalars
all have a pass-by-value phase somewhere in their lifecycle
(calling `nb.AssignString("x")`, then `n := nb.Build()`, then `nb.AssignString("y")`
won't error if `nb` is a freestanding builder for strings... but it also
won't result in mutating `n` to contain `"y"`, so overall, it's safe).

We could normalize the case with scalars at the root of a tree so that they
error more aggressively... but currently we haven't bothered, since this would
require adding another piece of memory to the scalar builders; and meanwhile
we're not in trouble on compositional correctness.

Note that these remarks are for the `basicnode` package, but may also
apply to other implementations too (e.g., our codegen output follows similar
overall logic).

### NodePrototypes are available through a singleton

Every NodePrototype available from this package is exposed as a field
in a struct of which there's one public exported instance available,
called 'Prototype'.

This means you can use it like this:

```go
nbm := basicnode.Prototype.Map.NewBuilder()
nbs := basicnode.Prototype.String.NewBuilder()
nba := basicnode.Prototype.Any.NewBuilder()
// etc
```

(If you're interested in the performance of this: it's free!
Methods called at the end of the chain are inlinable.
Since all of the types of the structures on the way there are zero-member
structs, the compiler can effectively treat them as constants,
and thus freely elide any memory dereferences that would
otherwise be necessary to get methods on such a value.)

### NodePrototypes are (also) available as exported concrete types

The 'Prototype' singleton is one way to access the NodePrototype in this package;
their exported types are another equivalent way.

```go
basicnode.Prototype.Map = basicnode.Prototype__Map{}
```

It is recommended to use the singleton style;
they compile to identical assembly, and the singleton is syntactically prettier.

We may make these concrete types unexported in the future.
A decision on this is deferred until some time has passed and
we can accumulate reasonable certainty that there's no need for an exported type
(such as type assertions, etc).
