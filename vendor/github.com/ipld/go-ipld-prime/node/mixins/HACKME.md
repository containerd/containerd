node mixins and how to use them
===============================

These mixins are here to:

1. reduce the amount of code you need to write to create a new Node implementation, and
2. standardize a lot of the error handling for common cases (especially, around kinds).

"Reduce the amount of code" also has an application in codegen,
where while it doesn't save any human effort, it does reduce GLOC size.
(Or more precisely, it doesn't save *lines*, since we use them in verbose style,
but it does make those lines an awful lot shorter.)

Note that these mixins are _not_ particularly here to help with performance.

- all `ErrWrongKind` error are returned by value, which means a `runtime.convT2I` which means a heap allocation.
  The error paths will therefore never be "fast"; it will *always* be cheaper
  to check `kind` in advance than to probe and handle errors, if efficiency is your goal.
- in general, there's really no way to improve upon the performance of having these methods simply writen directlyon your type.

These mixins will affect struct size if you use them via embed.
They can also be used without any effect on struct size if used more verbosely.

The binary/assembly output size is not affected by use of the mixins.
(If using them verbosely -- e.g. still declaring methods on your type
and using `return mixins.Kind{"TypeName"}.Method()` in the method body --
the end result is the inliner kicks in, and the end result is almost
identical binary size.)

Summary:

- SLOC: good, or neutral depending on use
- GLOC: good
- standardized: good
- speed: neutral
- mem size: neutral if used verbosely, bad if used most tersely
- asm size: neutral
