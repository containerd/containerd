/*
	Refmt is a serialization and object-mapping library.

	Look to the README on github for more high-level information.

	This top-level package exposes simple helper methods for most common operations.
	You can also compose custom marshallers/unmarshallers and serializer/deserializers
	by constructing a `refmt.TokenPump` with components from the packages beneath this one.
	For example, the `refmt.JsonEncode` helper method can be replicated by combining
	an `obj.Marshaller` with a `json.Encoder`.
*/
package refmt
