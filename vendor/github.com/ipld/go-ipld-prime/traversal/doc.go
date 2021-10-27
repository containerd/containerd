// This package provides functional utilities for traversing and transforming
// IPLD nodes.
//
// The traversal.Path type provides a description of how to perform
// several steps across a Node tree.  These are dual purpose:
// Paths can be used as instructions to do some traversal, and
// Paths are accumulated during traversals as a log of progress.
//
// "Focus" functions provide syntactic sugar for using ipld.Path to jump
// to a Node deep in a tree of other Nodes.
//
// "FocusedTransform" functions can do the same such deep jumps, and support
// mutation as well!
// (Of course, since ipld.Node is an immutable interface, more precisely
// speaking, "transformations" are implemented rebuilding trees of nodes to
// emulate mutation in a copy-on-write way.)
//
// "Walk" functions perform a walk of a Node graph, and apply visitor
// functions multiple Nodes.  The more advanced Walk functions can be guided
// by Selectors, which provide a declarative mechanism for guiding the
// traversal and filtering which Nodes are of interest.
// (See the selector sub-package for more detail.)
//
// "WalkTransforming" is similar to Traverse, but with support for mutations.
// Like "FocusTransform", "WalkTransforming" operates in a copy-on-write way.
//
// All of these functions -- the "Focus*" and "Walk*" family alike --
// work via callbacks: they do the traversal, and call a user-provided function
// with a handle to the reached Node.  Further "Focus" and "Walk" can be used
// recursively within this callback.
//
// All of these functions -- the "Focus*" and "Walk*" family alike --
// include support for automatic resolution and loading of new Node trees
// whenever IPLD Links are encountered.  This can be configured freely
// by providing LinkLoader interfaces to the traversal.Config.
//
// Some notes on the limits of usage:
//
// The "*Transform" family of methods is most appropriate for patterns of usage
// which resemble point mutations.
// More general transformations -- zygohylohistomorphisms, etc -- will be best
// implemented by composing the read-only systems (e.g. Focus, Traverse) and
// handling the accumulation in the visitor functions.
//
// (Why?  The "point mutation" use-case gets core library support because
// it's both high utility and highly clear how to implement it.
// More advanced transformations are nontrivial to provide generalized support
// for, for three reasons: efficiency is hard; not all existing research into
// categorical recursion schemes is necessarily applicable without modification
// (efficient behavior in a merkle-tree context is not the same as efficient
// behavior on uniform memory!); and we have the further compounding complexity
// of the range of choices available for underlying Node implementation.
// Therefore, attempts at generalization are not included here; handling these
// issues in concrete cases is easy, so we call it an application logic concern.
// However, exploring categorical recursion schemes as a library is encouraged!)
//
package traversal
