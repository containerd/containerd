/*
	Package implementing the CBOR -- Concise Binary Object Notation
	-- http://cbor.io/ -- spec.

	CBOR is more or less freely interchangable with json: it's schemaless,
	and composed of similar map and array types.
	However, CBOR is binary, length delimited -- and thus very fast to parse,
	and can store binary data without expansion problems -- and also distinguishes
	types like integer, string, float, and bytes all clearly from each other.

	The `cbor.Marshal` and `cbor.Unmarshal` functions are the quickest way
	to convert your Go objects to and from serial CBOR.

	The `cbor.NewMarshaller` and `cbor.NewUmarshaller` functions give a little
	more control.  If performance is important, prefer these; recycling
	the marshaller instances will significantly cut down on memory allocations
	and improve performance.

	The `*Atlased` variants of constructors allow you set up marshalling with
	an `refmt/obj/atlas.Atlas`, unlocking all of refmt's advanced features
	and custom object mapping powertools.

	The `cbor.Encoder` and `cbor.Decoder` types implement the low-level functionality
	of converting serial CBOR byte streams into refmt Token streams.
	Users don't usually need to use these directly.
*/
package cbor
