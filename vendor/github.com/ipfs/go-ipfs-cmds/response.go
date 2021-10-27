package cmds

import ()

// Response is the result of a command request. Response is returned to the client.
type Response interface {
	Request() *Request

	Error() *Error
	Length() uint64

	// Next returns the next emitted value.
	// The returned error can be a network or decoding error.
	Next() (interface{}, error)
}
