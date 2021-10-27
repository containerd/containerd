hackme: NodeBuilder and NodeAssembler behaviors
===============================================

high level rules of builders and assemblers
-------------------------------------------

- Errors should be returned as soon as possible.
	- That means an error like "repeated key in map" should be returned by the key assembler!
		- Either 'NodeAssembler.AssignString' should return this (for simple keys on untyped maps, or on structs, etc)...
		- ... or 'MapAssembler.Finish' (in the case of complex keys in a typed map).

- Logical integrity checks must be done locally -- recursive types rely on their contained types to report errors, and the recursive type wraps the assemblers of their contained type in order to check and correctly invalidate/rollback the recursive construction.

- Recursive types tend to have a value assembler that wraps the child type's assembler in order to intercept relevant "finish" methods.
	- This is generally where that logic integrity check mentioned above is tracked; we need explicit confirmation that it *passes* before the parent's assembly should proceed.
	- Implementations may also need this moment to complete any assignment of the child value into position in the parent value.  But not all implementations need this -- some will have had all the child assembler effects applying directly to the final memory positions.

- Assemblers should invalidate themselves as soon as they become "finished".
	- For maps and lists, that means the "Finish" methods.
	- For all the other scalars, the "Assign*" method itself means finished.
	- Or in other words: whatever method returns an `error`, that's what makes that assembler "finished".
	- The purpose of this is to prevent accidental mutation after any validations have been performed during the "finish" processing.

- Many methods must be called in the right order, and the user must not hold onto references after calling "finish" methods on them.
	- The reason this is important is to enable assembler systems to agressively reuse memory, thus increasing performance.
	- Thus, if you hold onto NodeAssembler reference after being finished with it... you can't assume it'll explicitly error if you call further methods on it, because it might now be operating again... _on a different target_.
		- In recursive structures, calling AssembleKey or AssembleValue might return pointer-identical assemblers (per warning in previous bullet), but the memory their assembly is targetted to should always advance -- it should never target already-assembled memory.
	- (If you're thinking "the Rust memory model would be able to greatly enhance safety here!"... yes.  Yes it would.)
	- When misuses of order are detected, these may cause panics (rather than error returns) (not all methods that can be so misused have error returns).


detailed rules and expectations for implementers
------------------------------------------------

The expectations in the "happy path" are often clear.
Here are also collected some details of exactly what should happen when an error has been reached,
but the caller tries to continue anyway.

- while building maps:
	- assigning a key with 'AssembleKey':
		- in case of success: clearly 'AssembleValue' should be ready to use next.
		- in case of failure from repeated key:
			- the error must be returned immediately from either the 'NodeAssembler.AssignString' or the 'MapAssembler.Finish' method.
				- 'AssignString' for any simple keys; 'MapAssembler.Finish' may be relevant in the case of complex keys in a typed map.
				- implementers take note: this implies the `NodeAssembler` returned by `AssembleKey` has some way to refer to the map assembler that spawned it.
			- no side effect should be visible if 'AssembleKey' is called again next.
				- (typically this doesn't require extra code for the string case, but it may require some active zeroing in the complex key case.)
				- (remember to reset any internal flag for expecting 'AssembleValue' to be used next, and decrement any length pointers that were optimistically incremented!)
				- n.b. the "no side effect" rule here is for keys, not for values.
					- TODO/REVIEW: do we want the no-side-effect rule for values?  it might require nontrivial volumes of zeroing, and often in practice, this might be wasteful.

- invalidation of assemblers:
	- is typically implemented by nil'ing the wip node they point to.
		- this means you get nil pointer dereference panics when attempting to use an assembler after it's finished... which is not the greatest error message.
		- but it does save us a lot of check code for a situation that the user certainly shouldn't get into in the first place.
		- (worth review: can we add that check code without extra runtime cost?  possibly, because the compiler might then skip its own implicit check branches.  might still increase SLOC noticably in codegen output, though.)
		- worth noting there's a limit to how good this can be anyway: it's "best effort" error reporting: see the remarks on reuse of assembler memory in "overall rules" above.
	- it's systemically critical to not yield an assembler _ever again in the future_ that refers to some memory already considered finished.
		- even though we no longer return intermediate nodes, there's still many ways this could produce problems.  For example, complicating (if not outright breaking) COW sharing of segments of data.
		- in most situations, we get this for free, because the child assembler methods only go "forward" -- there's no backing up, lists have no random index insertion or update support, and maps actively reject dupe keys.
		- if you *do* make a system which exposes any of those features... be very careful; you will probably need to start tracking "freeze" flags on the data in order to retain systemic sanity.
