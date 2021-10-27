package network

import "errors"

// ErrNoRemoteAddrs is returned when there are no addresses associated with a peer during a dial.
var ErrNoRemoteAddrs = errors.New("no remote addresses")

// ErrNoConn is returned when attempting to open a stream to a peer with the NoDial
// option and no usable connection is available.
var ErrNoConn = errors.New("no usable connection to peer")

// ErrTransientConn is returned when attempting to open a stream to a peer with only a transient
// connection, without specifying the UseTransient option.
var ErrTransientConn = errors.New("transient connection to peer")
