package ipld

import (
	"fmt"
	"hash"
	"io"
)

// LinkSystem is a struct that composes all the individual functions
// needed to load and store content addressed data using IPLD --
// encoding functions, hashing functions, and storage connections --
// and then offers the operations a user wants -- Store and Load -- as methods.
//
// Typically, the functions which are fields of LinkSystem are not used
// directly by users (except to set them, when creating the LinkSystem),
// and it's the higher level operations such as Store and Load that user code then calls.
//
// The most typical way to get a LinkSystem is from the linking/cid package,
// which has a factory function called DefaultLinkSystem.
// The LinkSystem returned by that function will be based on CIDs,
// and use the multicodec registry and multihash registry to select encodings and hashing mechanisms.
// The BlockWriteOpener and BlockReadOpener must still be provided by the user;
// otherwise, only the ComputeLink method will work.
//
// Some implementations of BlockWriteOpener and BlockReadOpener may be
// found in the storage package.  Applications are also free to write their own.
// Custom wrapping of BlockWriteOpener and BlockReadOpener are also common,
// and may be reasonable if one wants to build application features that are block-aware.
type LinkSystem struct {
	EncoderChooser     func(LinkPrototype) (Encoder, error)
	DecoderChooser     func(Link) (Decoder, error)
	HasherChooser      func(LinkPrototype) (hash.Hash, error)
	StorageWriteOpener BlockWriteOpener
	StorageReadOpener  BlockReadOpener
	TrustedStorage     bool
	NodeReifier        NodeReifier
}

// The following two types define the two directions of transform that a codec can be expected to perform:
// from Node to serial stream, and from serial stream to Node (via a NodeAssembler).
//
// You'll find a couple of implementations matching this shape in subpackages of 'codec' in this module
// (these are the handful of encoders and decoders we ship as "batteries included").
// Other encoder and decoder implementations can be found in other repositories/modules.
// It should also be easy to implement encodecs and decoders of your own!
//
// Encoder and Decoder functions can be used on their own, but are also often used via the LinkSystem construction,
// which handles all the other related operations necessary for a content-addressed storage system at once.
//
// Encoder and Decoder functions can be registered in the multicodec table in the `codec` package
// if they're providing functionality that matches the expectations for a multicodec identifier.
// This table will be used by some common EncoderChooser and DecoderChooser implementations
// (namely, the ones in LinkSystems produced by the `linking/cid` package).
type (
	// Encoder defines the shape of a function which traverses a Node tree
	// and emits its data in a serialized form into an io.Writer.
	//
	// The dual of Encoder is a Decoder, which takes a NodeAssembler
	// and fills it with deserialized data consumed from an io.Reader.
	// Typically, Decoder and Encoder functions will be found in pairs,
	// and will be expected to be able to round-trip each other's data.
	//
	// Encoder functions can be used directly.
	// Encoder functions are also often used via a LinkSystem when working with content-addressed storage.
	// LinkSystem methods will helpfully handle the entire process of traversing a Node tree,
	// encoding this data, hashing it, streaming it to the writer, and committing it -- all as one step.
	//
	// An Encoder works with Nodes.
	// If you have a native golang structure, and want to serialize it using an Encoder,
	// you'll need to figure out how to transform that golang structure into an ipld.Node tree first.
	//
	// It may be useful to understand "multicodecs" when working with Encoders.
	// In IPLD, a system called "multicodecs" is typically used to describe encoding foramts.
	// A "multicodec indicator" is a number which describes an encoding;
	// the Link implementations used in IPLD (CIDs) store a multicodec indicator in the Link;
	// and in this library, a multicodec registry exists in the `codec` package,
	// and can be used to associate a multicodec indicator number with an Encoder function.
	// The default EncoderChooser in a LinkSystem will use this multicodec registry to select Encoder functions.
	// However, you can construct a LinkSystem that uses any EncoderChooser you want.
	// It is also possible to have and use Encoder functions that aren't registered as a multicodec at all...
	// we just recommend being cautious of this, because it may make your data less recognizable
	// when working with other systems that use multicodec indicators as part of their communication.
	Encoder func(Node, io.Writer) error

	// Decoder defines the shape of a function which produces a Node tree
	// by reading serialized data from an io.Reader.
	// (Decoder doesn't itself return a Node directly, but rather takes a NodeAssembler as an argument,
	// because this allows the caller more control over the Node implementation,
	// as well as some control over allocations.)
	//
	// The dual of Decoder is an Encoder, which takes a Node and
	// emits its data in a serialized form into an io.Writer.
	// Typically, Decoder and Encoder functions will be found in pairs,
	// and will be expected to be able to round-trip each other's data.
	//
	// Decoder functions can be used directly.
	// Decoder functions are also often used via a LinkSystem when working with content-addressed storage.
	// LinkSystem methods will helpfully handle the entire process of opening block readers,
	// verifying the hash of the data stream, and applying a Decoder to build Nodes -- all as one step.
	//
	// A Decoder works with Nodes.
	// If you have a native golang structure, and want to populate it with data using a Decoder,
	// you'll need to either get a NodeAssembler which proxies data into that structure directly,
	// or assemble a Node as intermediate storage and copy the data to the native structure as a separate step.
	//
	// It may be useful to understand "multicodecs" when working with Decoders.
	// See the documentation on the Encoder function interface for more discussion of multicodecs,
	// the multicodec table, and how this is typically connected to linking.
	Decoder func(NodeAssembler, io.Reader) error
)

// The following three types are the key functionality we need from a "blockstore".
//
// Some libraries might provide a "blockstore" object that has these as methods;
// it may also have more methods (like enumeration features, GC features, etc),
// but IPLD doesn't generally concern itself with those.
// We just need these key things, so we can "put" and "get".
//
// The functions are a tad more complicated than "put" and "get" so that they have good mechanical sympathy.
// In particular, the writing/"put" side is broken into two phases, so that the abstraction
// makes it easy to begin to write data before the hash that will identify it is fully computed.
type (
	// BlockReadOpener defines the shape of a function used to
	// open a reader for a block of data.
	//
	// In a content-addressed system, the Link parameter should be only
	// determiner of what block body is returned.
	//
	// The LinkContext may be zero, or may be used to carry extra information:
	// it may be used to carry info which hints at different storage pools;
	// it may be used to carry authentication data; etc.
	// (Any such behaviors are something that a BlockReadOpener implementation
	// will needs to document at a higher detail level than this interface specifies.
	// In this interface, we can only note that it is possible to pass such information opaquely
	// via the LinkContext or by attachments to the general-purpose Context it contains.)
	// The LinkContext should not have effect on the block body returned, however;
	// at most should only affect data availability
	// (e.g. whether any block body is returned, versus an error).
	//
	// Reads are cancellable by cancelling the LinkContext.Context.
	//
	// Other parts of the IPLD library suite (such as the traversal package, and all its functions)
	// will typically take a Context as a parameter or piece of config from the caller,
	// and will pass that down through the LinkContext, meaning this can be used to
	// carry information as well as cancellation control all the way through the system.
	//
	// BlockReadOpener is typically not used directly, but is instead
	// composed in a LinkSystem and used via the methods of LinkSystem.
	// LinkSystem methods will helpfully handle the entire process of opening block readers,
	// verifying the hash of the data stream, and applying a Decoder to build Nodes -- all as one step.
	//
	// BlockReadOpener implementations are not required to validate that
	// the contents which will be streamed out of the reader actually match
	// and hash in the Link parameter before returning.
	// (This is something that the LinkSystem composition will handle if you're using it.)
	//
	// Some implementations of BlockWriteOpener and BlockReadOpener may be
	// found in the storage package.  Applications are also free to write their own.
	BlockReadOpener func(LinkContext, Link) (io.Reader, error)

	// BlockWriteOpener defines the shape of a function used to open a writer
	// into which data can be streamed, and which will eventually be "commited".
	// Committing is done using the BlockWriteCommitter returned by using the BlockWriteOpener,
	// and finishes the write along with requiring stating the Link which should identify this data for future reading.
	//
	// The LinkContext may be zero, or may be used to carry extra information:
	// it may be used to carry info which hints at different storage pools;
	// it may be used to carry authentication data; etc.
	//
	// Writes are cancellable by cancelling the LinkContext.Context.
	//
	// Other parts of the IPLD library suite (such as the traversal package, and all its functions)
	// will typically take a Context as a parameter or piece of config from the caller,
	// and will pass that down through the LinkContext, meaning this can be used to
	// carry information as well as cancellation control all the way through the system.
	//
	// BlockWriteOpener is typically not used directly, but is instead
	// composed in a LinkSystem and used via the methods of LinkSystem.
	// LinkSystem methods will helpfully handle the entire process of traversing a Node tree,
	// encoding this data, hashing it, streaming it to the writer, and committing it -- all as one step.
	//
	// BlockWriteOpener implementations are expected to start writing their content immediately,
	// and later, the returned BlockWriteCommitter should also be able to expect that
	// the Link which it is given is a reasonable hash of the content.
	// (To give an example of how this might be efficiently implemented:
	// One might imagine that if implementing a disk storage mechanism,
	// the io.Writer returned from a BlockWriteOpener will be writing a new tempfile,
	// and when the BlockWriteCommiter is called, it will flush the writes
	// and then use a rename operation to place the tempfile in a permanent path based the Link.)
	//
	// Some implementations of BlockWriteOpener and BlockReadOpener may be
	// found in the storage package.  Applications are also free to write their own.
	BlockWriteOpener func(LinkContext) (io.Writer, BlockWriteCommitter, error)

	// BlockWriteCommitter defines the shape of a function which, together
	// with BlockWriteOpener, handles the writing and "committing" of a write
	// to a content-addressable storage system.
	//
	// BlockWriteCommitter is a function which is will be called at the end of a write process.
	// It should flush any buffers and close the io.Writer which was
	// made available earlier from the BlockWriteOpener call that also returned this BlockWriteCommitter.
	//
	// BlockWriteCommitter takes a Link parameter.
	// This Link is expected to be a reasonable hash of the content,
	// so that the BlockWriteCommitter can use this to commit the data to storage
	// in a content-addressable fashion.
	// See the documentation of BlockWriteOpener for more description of this
	// and an example of how this is likely to be reduced to practice.
	BlockWriteCommitter func(Link) error

	// NodeReifier defines the shape of a function that given a node with no schema
	// or a basic schema, constructs Advanced Data Layout node
	//
	// The LinkSystem itself is passed to the NodeReifier along with a link context
	// because Node interface methods on an ADL may actually traverse links to other
	// pieces of context addressed data that need to be loaded with the Link system
	//
	// A NodeReifier return one of three things:
	// - original node, no error = no reification occurred, just use original node
	// - reified node, no error = the simple node was converted to an ADL
	// - nil, error = the simple node should have been converted to an ADL but something
	// went wrong when we tried to do so
	//
	NodeReifier func(LinkContext, Node, *LinkSystem) (Node, error)
)

// ErrLinkingSetup is returned by methods on LinkSystem when some part of the system is not set up correctly,
// or when one of the components refuses to handle a Link or LinkPrototype given.
// (It is not yielded for errors from the storage nor codec systems once they've started; those errors rise without interference.)
type ErrLinkingSetup struct {
	Detail string // Perhaps an enum here as well, which states which internal function was to blame?
	Cause  error
}

func (e ErrLinkingSetup) Error() string { return fmt.Sprintf("%s: %v", e.Detail, e.Cause) }
func (e ErrLinkingSetup) Unwrap() error { return e.Cause }
