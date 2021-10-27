// Package routing provides interfaces for peer routing and content routing in libp2p.
package routing

import (
	"context"
	"errors"

	ci "github.com/libp2p/go-libp2p-core/crypto"
	"github.com/libp2p/go-libp2p-core/peer"

	cid "github.com/ipfs/go-cid"
)

// ErrNotFound is returned when the router fails to find the requested record.
var ErrNotFound = errors.New("routing: not found")

// ErrNotSupported is returned when the router doesn't support the given record
// type/operation.
var ErrNotSupported = errors.New("routing: operation or key not supported")

// ContentRouting is a value provider layer of indirection. It is used to find
// information about who has what content.
//
// Content is identified by CID (content identifier), which encodes a hash
// of the identified content in a future-proof manner.
type ContentRouting interface {
	// Provide adds the given cid to the content routing system. If 'true' is
	// passed, it also announces it, otherwise it is just kept in the local
	// accounting of which objects are being provided.
	Provide(context.Context, cid.Cid, bool) error

	// Search for peers who are able to provide a given key
	//
	// When count is 0, this method will return an unbounded number of
	// results.
	FindProvidersAsync(context.Context, cid.Cid, int) <-chan peer.AddrInfo
}

// PeerRouting is a way to find address information about certain peers.
// This can be implemented by a simple lookup table, a tracking server,
// or even a DHT.
type PeerRouting interface {
	// FindPeer searches for a peer with given ID, returns a peer.AddrInfo
	// with relevant addresses.
	FindPeer(context.Context, peer.ID) (peer.AddrInfo, error)
}

// ValueStore is a basic Put/Get interface.
type ValueStore interface {

	// PutValue adds value corresponding to given Key.
	PutValue(context.Context, string, []byte, ...Option) error

	// GetValue searches for the value corresponding to given Key.
	GetValue(context.Context, string, ...Option) ([]byte, error)

	// SearchValue searches for better and better values from this value
	// store corresponding to the given Key. By default implementations must
	// stop the search after a good value is found. A 'good' value is a value
	// that would be returned from GetValue.
	//
	// Useful when you want a result *now* but still want to hear about
	// better/newer results.
	//
	// Implementations of this methods won't return ErrNotFound. When a value
	// couldn't be found, the channel will get closed without passing any results
	SearchValue(context.Context, string, ...Option) (<-chan []byte, error)
}

// Routing is the combination of different routing types supported by libp2p.
// It can be satisfied by a single item (such as a DHT) or multiple different
// pieces that are more optimized to each task.
type Routing interface {
	ContentRouting
	PeerRouting
	ValueStore

	// Bootstrap allows callers to hint to the routing system to get into a
	// Boostrapped state and remain there. It is not a synchronous call.
	Bootstrap(context.Context) error

	// TODO expose io.Closer or plain-old Close error
}

// PubKeyFetcher is an interfaces that should be implemented by value stores
// that can optimize retrieval of public keys.
//
// TODO(steb): Consider removing, see https://github.com/libp2p/go-libp2p-routing/issues/22.
type PubKeyFetcher interface {
	// GetPublicKey returns the public key for the given peer.
	GetPublicKey(context.Context, peer.ID) (ci.PubKey, error)
}

// KeyForPublicKey returns the key used to retrieve public keys
// from a value store.
func KeyForPublicKey(id peer.ID) string {
	return "/pk/" + string(id)
}

// GetPublicKey retrieves the public key associated with the given peer ID from
// the value store.
//
// If the ValueStore is also a PubKeyFetcher, this method will call GetPublicKey
// (which may be better optimized) instead of GetValue.
func GetPublicKey(r ValueStore, ctx context.Context, p peer.ID) (ci.PubKey, error) {
	switch k, err := p.ExtractPublicKey(); err {
	case peer.ErrNoPublicKey:
		// check the datastore
	case nil:
		return k, nil
	default:
		return nil, err
	}

	if dht, ok := r.(PubKeyFetcher); ok {
		// If we have a DHT as our routing system, use optimized fetcher
		return dht.GetPublicKey(ctx, p)
	}
	key := KeyForPublicKey(p)
	pkval, err := r.GetValue(ctx, key)
	if err != nil {
		return nil, err
	}

	// get PublicKey from node.Data
	return ci.UnmarshalPublicKey(pkval)
}
