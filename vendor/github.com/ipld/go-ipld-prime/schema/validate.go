package schema

/*
	Okay, so.  There are several fun considerations for a "validate" method.

	---

	There's two radically different approaches to "validate"/"reify":

	- Option 1: Look at the schema.Type info and check if a data node seems
	  to match it -- recursing on the type info.
	- Option 2: Use the schema.Type{}.RepresentationNodeBuilder() to feed data
      into it -- recursing on what the nodebuilder already expresses.

	(Option 2 also need to take a `memStorage ipld.NodeBuilder` param, btw,
	for handling all the cases where we *aren't* doing codegen.)

	Option 1 provides a little more opportunity for returning multiple errors.
	Option 2 will generally have a hard time with that (nodebuilers are not
	necessarily in a valid state after their first error encounter).

	As a result of having these two options at all, we may indeed end up with
	at least two very different functions -- despite seeming to do similar
	things, their interior will radically diverge.

	---

	We may also need to consider distinct reification paths: we may want one
	that returns a new node tree which is eagerly converted to schema.TypedNode
	recursively; and another that returns a lazyNode which wraps things
	with their typed node constraints only as they're requested.
	(Note that the latter would have interesting implications for any code
	which has expectations about pointer equality consistency.)

	---

	A further fun issue which needs consideration: well, I'll just save a snip
	of prospective docs I wrote while trying to iterate on these functions:

		// Note that using Validate on a node that's already a schema.TypedNode is likely
		// to be nonsensical.  In many schemas, the schema.TypedNode tree is actually a
		// different depth than its representational tree (e.g. unions can cause this),

	... and that's ... that's a fairly sizable issue that needs resolving.
	There's a couple of different ways to handle some of the behaviors around
	unions, and some of them make the tradeoff described above, and I'm really
	unsure if all the implications have been sussed out yet.  We should defer
	writing code that depends on this issue until gathering some more info.

	---

	One more note: about returning multiple errors from a Validate function:
	there's an upper bound of the utility of the thing.  Going farther than the
	first parse error is nice, but it will still hit limits: for example,
	upon encountering a union and failing to match it, we can't generally
	produce further errors from anywhere deeper in the tree without them being
	combinatorial "if previous juncture X was type Y, then..." nonsense.
	(This applies to all recursive kinds to some degree, but it's especially
	rough with unions.  For most of the others, it's flatly a missing field,
	or an excessive field, or a leaf error; with unions it can be hard to tell.)

	---

	And finally: both "Validate" and "Reify" methods might actually belong
	in the schema.TypedNode package -- if they make *any* reference to `schema.TypedNode`,
	then they have no choice (otherwise, cyclic imports would occur).
	If we make a "Validate" that works purely on the schema.Type info, and
	returns *only* errors: only then we can have it in the schema package.

*/
