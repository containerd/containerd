// Package peerstore provides types and interfaces for local storage of address information,
// metadata, and public key material about libp2p peers.
package peerstore

import (
	"context"
	"errors"
	"io"
	"math"
	"time"

	ic "github.com/libp2p/go-libp2p-core/crypto"
	"github.com/libp2p/go-libp2p-core/peer"
	"github.com/libp2p/go-libp2p-core/record"

	ma "github.com/multiformats/go-multiaddr"
)

var ErrNotFound = errors.New("item not found")

var (
	// AddressTTL is the expiration time of addresses.
	AddressTTL = time.Hour

	// TempAddrTTL is the ttl used for a short lived address.
	TempAddrTTL = time.Minute * 2

	// ProviderAddrTTL is the TTL of an address we've received from a provider.
	// This is also a temporary address, but lasts longer. After this expires,
	// the records we return will require an extra lookup.
	ProviderAddrTTL = time.Minute * 10

	// RecentlyConnectedAddrTTL is used when we recently connected to a peer.
	// It means that we are reasonably certain of the peer's address.
	RecentlyConnectedAddrTTL = time.Minute * 10

	// OwnObservedAddrTTL is used for our own external addresses observed by peers.
	OwnObservedAddrTTL = time.Minute * 10
)

// Permanent TTLs (distinct so we can distinguish between them, constant as they
// are, in fact, permanent)
const (
	// PermanentAddrTTL is the ttl for a "permanent address" (e.g. bootstrap nodes).
	PermanentAddrTTL = math.MaxInt64 - iota

	// ConnectedAddrTTL is the ttl used for the addresses of a peer to whom
	// we're connected directly. This is basically permanent, as we will
	// clear them + re-add under a TempAddrTTL after disconnecting.
	ConnectedAddrTTL
)

// Peerstore provides a threadsafe store of Peer related
// information.
type Peerstore interface {
	io.Closer

	AddrBook
	KeyBook
	PeerMetadata
	Metrics
	ProtoBook

	// PeerInfo returns a peer.PeerInfo struct for given peer.ID.
	// This is a small slice of the information Peerstore has on
	// that peer, useful to other services.
	PeerInfo(peer.ID) peer.AddrInfo

	// Peers returns all of the peer IDs stored across all inner stores.
	Peers() peer.IDSlice
}

// PeerMetadata can handle values of any type. Serializing values is
// up to the implementation. Dynamic type introspection may not be
// supported, in which case explicitly enlisting types in the
// serializer may be required.
//
// Refer to the docs of the underlying implementation for more
// information.
type PeerMetadata interface {
	// Get/Put is a simple registry for other peer-related key/value pairs.
	// if we find something we use often, it should become its own set of
	// methods. this is a last resort.
	Get(p peer.ID, key string) (interface{}, error)
	Put(p peer.ID, key string, val interface{}) error
}

// AddrBook holds the multiaddrs of peers.
type AddrBook interface {

	// AddAddr calls AddAddrs(p, []ma.Multiaddr{addr}, ttl)
	AddAddr(p peer.ID, addr ma.Multiaddr, ttl time.Duration)

	// AddAddrs gives this AddrBook addresses to use, with a given ttl
	// (time-to-live), after which the address is no longer valid.
	// If the manager has a longer TTL, the operation is a no-op for that address
	AddAddrs(p peer.ID, addrs []ma.Multiaddr, ttl time.Duration)

	// SetAddr calls mgr.SetAddrs(p, addr, ttl)
	SetAddr(p peer.ID, addr ma.Multiaddr, ttl time.Duration)

	// SetAddrs sets the ttl on addresses. This clears any TTL there previously.
	// This is used when we receive the best estimate of the validity of an address.
	SetAddrs(p peer.ID, addrs []ma.Multiaddr, ttl time.Duration)

	// UpdateAddrs updates the addresses associated with the given peer that have
	// the given oldTTL to have the given newTTL.
	UpdateAddrs(p peer.ID, oldTTL time.Duration, newTTL time.Duration)

	// Addrs returns all known (and valid) addresses for a given peer.
	Addrs(p peer.ID) []ma.Multiaddr

	// AddrStream returns a channel that gets all addresses for a given
	// peer sent on it. If new addresses are added after the call is made
	// they will be sent along through the channel as well.
	AddrStream(context.Context, peer.ID) <-chan ma.Multiaddr

	// ClearAddresses removes all previously stored addresses.
	ClearAddrs(p peer.ID)

	// PeersWithAddrs returns all of the peer IDs stored in the AddrBook.
	PeersWithAddrs() peer.IDSlice
}

// CertifiedAddrBook manages "self-certified" addresses for remote peers.
// Self-certified addresses are contained in peer.PeerRecords
// which are wrapped in a record.Envelope and signed by the peer
// to whom they belong.
//
// Certified addresses (CA) are generally more secure than uncertified
// addresses (UA). Consequently, CAs beat and displace UAs. When the
// peerstore learns CAs for a peer, it will reject UAs for the same peer
// (as long as the former haven't expired).
// Furthermore, peer records act like sequenced snapshots of CAs. Therefore,
// processing a peer record that's newer than the last one seen overwrites
// all addresses with the incoming ones.
//
// This interface is most useful when combined with AddrBook.
// To test whether a given AddrBook / Peerstore implementation supports
// certified addresses, callers should use the GetCertifiedAddrBook helper or
// type-assert on the CertifiedAddrBook interface:
//
//     if cab, ok := aPeerstore.(CertifiedAddrBook); ok {
//         cab.ConsumePeerRecord(signedPeerRecord, aTTL)
//     }
//
type CertifiedAddrBook interface {
	// ConsumePeerRecord adds addresses from a signed peer.PeerRecord (contained in
	// a record.Envelope), which will expire after the given TTL.
	//
	// The 'accepted' return value indicates that the record was successfully processed
	// and integrated into the CertifiedAddrBook state. If 'accepted' is false but no
	// error is returned, it means that the record was ignored, most likely because
	// a newer record exists for the same peer.
	//
	// Signed records added via this method will be stored without
	// alteration as long as the address TTLs remain valid. The Envelopes
	// containing the PeerRecords can be retrieved by calling GetPeerRecord(peerID).
	//
	// If the signed PeerRecord belongs to a peer that already has certified
	// addresses in the CertifiedAddrBook, the new addresses will replace the
	// older ones, if the new record has a higher sequence number than the
	// existing record. Attempting to add a peer record with a
	// sequence number that's <= an existing record for the same peer will not
	// result in an error, but the record will be ignored, and the 'accepted'
	// bool return value will be false.
	//
	// If the CertifiedAddrBook is also an AddrBook (which is most likely the case),
	// adding certified addresses for a peer will *replace* any
	// existing non-certified addresses for that peer, and only the certified
	// addresses will be returned from AddrBook.Addrs thereafter.
	//
	// Likewise, once certified addresses have been added for a given peer,
	// any non-certified addresses added via AddrBook.AddAddrs or
	// AddrBook.SetAddrs will be ignored. AddrBook.SetAddrs may still be used
	// to update the TTL of certified addresses that have previously been
	// added via ConsumePeerRecord.
	ConsumePeerRecord(s *record.Envelope, ttl time.Duration) (accepted bool, err error)

	// GetPeerRecord returns a Envelope containing a PeerRecord for the
	// given peer id, if one exists.
	// Returns nil if no signed PeerRecord exists for the peer.
	GetPeerRecord(p peer.ID) *record.Envelope
}

// GetCertifiedAddrBook is a helper to "upcast" an AddrBook to a
// CertifiedAddrBook by using type assertion. If the given AddrBook
// is also a CertifiedAddrBook, it will be returned, and the ok return
// value will be true. Returns (nil, false) if the AddrBook is not a
// CertifiedAddrBook.
//
// Note that since Peerstore embeds the AddrBook interface, you can also
// call GetCertifiedAddrBook(myPeerstore).
func GetCertifiedAddrBook(ab AddrBook) (cab CertifiedAddrBook, ok bool) {
	cab, ok = ab.(CertifiedAddrBook)
	return cab, ok
}

// KeyBook tracks the keys of Peers.
type KeyBook interface {
	// PubKey stores the public key of a peer.
	PubKey(peer.ID) ic.PubKey

	// AddPubKey stores the public key of a peer.
	AddPubKey(peer.ID, ic.PubKey) error

	// PrivKey returns the private key of a peer, if known. Generally this might only be our own
	// private key, see
	// https://discuss.libp2p.io/t/what-is-the-purpose-of-having-map-peer-id-privatekey-in-peerstore/74.
	PrivKey(peer.ID) ic.PrivKey

	// AddPrivKey stores the private key of a peer.
	AddPrivKey(peer.ID, ic.PrivKey) error

	// PeersWithKeys returns all the peer IDs stored in the KeyBook
	PeersWithKeys() peer.IDSlice
}

// Metrics is just an object that tracks metrics
// across a set of peers.
type Metrics interface {
	// RecordLatency records a new latency measurement
	RecordLatency(peer.ID, time.Duration)

	// LatencyEWMA returns an exponentially-weighted moving avg.
	// of all measurements of a peer's latency.
	LatencyEWMA(peer.ID) time.Duration
}

// ProtoBook tracks the protocols supported by peers.
type ProtoBook interface {
	GetProtocols(peer.ID) ([]string, error)
	AddProtocols(peer.ID, ...string) error
	SetProtocols(peer.ID, ...string) error
	RemoveProtocols(peer.ID, ...string) error

	// SupportsProtocols returns the set of protocols the peer supports from among the given protocols.
	// If the returned error is not nil, the result is indeterminate.
	SupportsProtocols(peer.ID, ...string) ([]string, error)

	// FirstSupportedProtocol returns the first protocol that the peer supports among the given protocols.
	// If the peer does not support any of the given protocols, this function will return an empty string and a nil error.
	// If the returned error is not nil, the result is indeterminate.
	FirstSupportedProtocol(peer.ID, ...string) (string, error)
}
