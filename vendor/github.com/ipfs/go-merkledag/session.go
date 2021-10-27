package merkledag

import (
	"context"

	ipld "github.com/ipfs/go-ipld-format"
)

// SessionMaker is an object that can generate a new fetching session.
type SessionMaker interface {
	Session(context.Context) ipld.NodeGetter
}

// NewSession returns a session backed NodeGetter if the given NodeGetter
// implements SessionMaker.
func NewSession(ctx context.Context, g ipld.NodeGetter) ipld.NodeGetter {
	if sm, ok := g.(SessionMaker); ok {
		return sm.Session(ctx)
	}
	return g
}
