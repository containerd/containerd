/*
	Package implementing the JSON -- http://json.org/ -- spec.

	The `json.Marshal` and `json.Unmarshal` functions are the quickest way
	to convert your Go objects to and from serial JSON.

	The `json.NewMarshaller` and `json.NewUmarshaller` functions give a little
	more control.  If performance is important, prefer these; recycling
	the marshaller instances will significantly cut down on memory allocations
	and improve performance.

	The `*Atlased` variants of constructors allow you set up marshalling with
	an `refmt/obj/atlas.Atlas`, unlocking all of refmt's advanced features
	and custom object mapping powertools.

	The `json.Encoder` and `json.Decoder` types implement the low-level functionality
	of converting serial JSON byte streams into refmt Token streams.
	Users don't usually need to use these directly.
*/
package json
