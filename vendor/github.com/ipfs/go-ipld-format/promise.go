package format

import (
	"context"
)

// NodePromise provides a promise like interface for a dag Node
// the first call to Get will block until the Node is received
// from its internal channels, subsequent calls will return the
// cached node.
//
// Thread Safety: This is multiple-consumer/single-producer safe.
func NewNodePromise(ctx context.Context) *NodePromise {
	return &NodePromise{
		done: make(chan struct{}),
		ctx:  ctx,
	}
}

type NodePromise struct {
	value Node
	err   error
	done  chan struct{}

	ctx context.Context
}

// Call this function to fail a promise.
//
// Once a promise has been failed or fulfilled, further attempts to fail it will
// be silently dropped.
func (np *NodePromise) Fail(err error) {
	if np.err != nil || np.value != nil {
		// Already filled.
		return
	}
	np.err = err
	close(np.done)
}

// Fulfill this promise.
//
// Once a promise has been fulfilled or failed, calling this function will
// panic.
func (np *NodePromise) Send(nd Node) {
	// if promise has a value, don't fail it
	if np.err != nil || np.value != nil {
		panic("already filled")
	}
	np.value = nd
	close(np.done)
}

// Get the value of this promise.
//
// This function is safe to call concurrently from any number of goroutines.
func (np *NodePromise) Get(ctx context.Context) (Node, error) {
	select {
	case <-np.done:
		return np.value, np.err
	case <-np.ctx.Done():
		return nil, np.ctx.Err()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
