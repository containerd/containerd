/*
	Package cmds helps building both standalone and client-server
	applications.

	Semantics

	The basic building blocks are requests, commands, emitters and
	responses. A command consists of a description of the
	parameters and a function. The function is passed the request
	as well as an emitter as arguments. It does operations on the
	inputs and sends the results to the user by emitting them.

	There are a number of emitters in this package and
	subpackages, but the user is free to create their own.

	Commands

	A command is a struct containing the commands help text, a
	description of the arguments and options, the command's
	processing function and a type to let the caller know what
	type will be emitted. Optionally one of the functions PostRun
	and Encoder may be defined that consumes the function's
	emitted values and generates a visual representation for e.g.
	the terminal. Encoders work on a value-by-value basis, while
	PostRun operates on the value stream.

	Emitters



	An emitter has the Emit method, that takes the command's
	function's output as an argument and passes it to the user.

		type ResponseEmitter interface {
			Close() error
			CloseWithError(error) error
			SetLength(length uint64)
			Emit(value interface{}) error
		}

	The command's function does not know what kind of emitter it
	works with, so the same function may run locally or on a
	server, using an rpc interface. Emitters can also send errors
	using the SetError method.

	The user-facing emitter usually is the cli emitter. Values
	emitter here will be printed to the terminal using either the
	Encoders or the PostRun function.

	Responses


	A response is a value that the user can read emitted values
	from.

		type Response interface {
			Request() Request
			Error() *Error
			Length() uint64
			Next() (interface{}, error)
		}

	Responses have a method Next() that returns the next
	emitted value and an error value. If the last element has been
	received, the returned error value is io.EOF. If the
	application code has sent an error using SetError, the error
	ErrRcvdError is returned on next, indicating that the caller
	should call Error(). Depending on the reponse type, other
	errors may also occur.

	Pipes

	Pipes are pairs (emitter, response), such that a value emitted
	on the emitter can be received in the response value. Most
	builtin emitters are "pipe" emitters. The most prominent
	examples are the channel pipe and the http pipe.

	The channel pipe is backed by a channel. The only error value
	returned by the response is io.EOF, which happens when the
	channel is closed.

	The http pipe is backed by an http connection. The response
	can also return other errors, e.g. if there are errors on the
	network.

	Examples

	To get a better idea of what's going on, take a look at the
	examples at
	https://github.com/ipfs/go-ipfs-cmds/tree/master/examples.

*/
package cmds
