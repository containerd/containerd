// Package protocol provides core interfaces for protocol routing and negotiation in libp2p.
package protocol

import (
	"io"
)

// HandlerFunc is a user-provided function used by the Router to
// handle a protocol/stream.
//
// Will be invoked with the protocol ID string as the first argument,
// which may differ from the ID used for registration if the handler
// was registered using a match function.
type HandlerFunc = func(protocol string, rwc io.ReadWriteCloser) error

// Router is an interface that allows users to add and remove protocol handlers,
// which will be invoked when incoming stream requests for registered protocols
// are accepted.
//
// Upon receiving an incoming stream request, the Router will check all registered
// protocol handlers to determine which (if any) is capable of handling the stream.
// The handlers are checked in order of registration; if multiple handlers are
// eligible, only the first to be registered will be invoked.
type Router interface {

	// AddHandler registers the given handler to be invoked for
	// an exact literal match of the given protocol ID string.
	AddHandler(protocol string, handler HandlerFunc)

	// AddHandlerWithFunc registers the given handler to be invoked
	// when the provided match function returns true.
	//
	// The match function will be invoked with an incoming protocol
	// ID string, and should return true if the handler supports
	// the protocol. Note that the protocol ID argument is not
	// used for matching; if you want to match the protocol ID
	// string exactly, you must check for it in your match function.
	AddHandlerWithFunc(protocol string, match func(string) bool, handler HandlerFunc)

	// RemoveHandler removes the registered handler (if any) for the
	// given protocol ID string.
	RemoveHandler(protocol string)

	// Protocols returns a list of all registered protocol ID strings.
	// Note that the Router may be able to handle protocol IDs not
	// included in this list if handlers were added with match functions
	// using AddHandlerWithFunc.
	Protocols() []string
}

// Negotiator is a component capable of reaching agreement over what protocols
// to use for inbound streams of communication.
type Negotiator interface {

	// NegotiateLazy will return the registered protocol handler to use
	// for a given inbound stream, returning as soon as the protocol has been
	// determined. Returns an error if negotiation fails.
	//
	// NegotiateLazy may return before all protocol negotiation responses have been
	// written to the stream. This is in contrast to Negotiate, which will block until
	// the Negotiator is finished with the stream.
	NegotiateLazy(rwc io.ReadWriteCloser) (io.ReadWriteCloser, string, HandlerFunc, error)

	// Negotiate will return the registered protocol handler to use for a given
	// inbound stream, returning after the protocol has been determined and the
	// Negotiator has finished using the stream for negotiation. Returns an
	// error if negotiation fails.
	Negotiate(rwc io.ReadWriteCloser) (string, HandlerFunc, error)

	// Handle calls Negotiate to determine which protocol handler to use for an
	// inbound stream, then invokes the protocol handler function, passing it
	// the protocol ID and the stream. Returns an error if negotiation fails.
	Handle(rwc io.ReadWriteCloser) error
}

// Switch is the component responsible for "dispatching" incoming stream requests to
// their corresponding stream handlers. It is both a Negotiator and a Router.
type Switch interface {
	Router
	Negotiator
}
