// Package mux provides stream multiplexing interfaces for libp2p.
//
// For a conceptual overview of stream multiplexing in libp2p, see
// https://docs.libp2p.io/concepts/stream-multiplexing/
package mux

import (
	"context"
	"errors"
	"io"
	"net"
	"time"
)

// ErrReset is returned when reading or writing on a reset stream.
var ErrReset = errors.New("stream reset")

// Stream is a bidirectional io pipe within a connection.
type MuxedStream interface {
	io.Reader
	io.Writer

	// Close closes the stream.
	//
	// * Any buffered data for writing will be flushed.
	// * Future reads will fail.
	// * Any in-progress reads/writes will be interrupted.
	//
	// Close may be asynchronous and _does not_ guarantee receipt of the
	// data.
	//
	// Close closes the stream for both reading and writing.
	// Close is equivalent to calling `CloseRead` and `CloseWrite`. Importantly, Close will not wait for any form of acknowledgment.
	// If acknowledgment is required, the caller must call `CloseWrite`, then wait on the stream for a response (or an EOF),
	// then call Close() to free the stream object.
	//
	// When done with a stream, the user must call either Close() or `Reset()` to discard the stream, even after calling `CloseRead` and/or `CloseWrite`.
	io.Closer

	// CloseWrite closes the stream for writing but leaves it open for
	// reading.
	//
	// CloseWrite does not free the stream, users must still call Close or
	// Reset.
	CloseWrite() error

	// CloseRead closes the stream for reading but leaves it open for
	// writing.
	//
	// When CloseRead is called, all in-progress Read calls are interrupted with a non-EOF error and
	// no further calls to Read will succeed.
	//
	// The handling of new incoming data on the stream after calling this function is implementation defined.
	//
	// CloseRead does not free the stream, users must still call Close or
	// Reset.
	CloseRead() error

	// Reset closes both ends of the stream. Use this to tell the remote
	// side to hang up and go away.
	Reset() error

	SetDeadline(time.Time) error
	SetReadDeadline(time.Time) error
	SetWriteDeadline(time.Time) error
}

// NoopHandler do nothing. Resets streams as soon as they are opened.
var NoopHandler = func(s MuxedStream) { s.Reset() }

// MuxedConn represents a connection to a remote peer that has been
// extended to support stream multiplexing.
//
// A MuxedConn allows a single net.Conn connection to carry many logically
// independent bidirectional streams of binary data.
//
// Together with network.ConnSecurity, MuxedConn is a component of the
// transport.CapableConn interface, which represents a "raw" network
// connection that has been "upgraded" to support the libp2p capabilities
// of secure communication and stream multiplexing.
type MuxedConn interface {
	// Close closes the stream muxer and the the underlying net.Conn.
	io.Closer

	// IsClosed returns whether a connection is fully closed, so it can
	// be garbage collected.
	IsClosed() bool

	// OpenStream creates a new stream.
	OpenStream(context.Context) (MuxedStream, error)

	// AcceptStream accepts a stream opened by the other side.
	AcceptStream() (MuxedStream, error)
}

// Multiplexer wraps a net.Conn with a stream multiplexing
// implementation and returns a MuxedConn that supports opening
// multiple streams over the underlying net.Conn
type Multiplexer interface {

	// NewConn constructs a new connection
	NewConn(c net.Conn, isServer bool) (MuxedConn, error)
}
