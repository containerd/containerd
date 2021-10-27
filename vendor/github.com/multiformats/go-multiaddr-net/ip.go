package manet

import (
	ma "github.com/multiformats/go-multiaddr"
	upstream "github.com/multiformats/go-multiaddr/net"
)

var (
	// Deprecated: use github.com/multiformats/go-multiaddr/net
	IP4Loopback = upstream.IP4Loopback

	// Deprecated: use github.com/multiformats/go-multiaddr/net
	IP6Loopback = upstream.IP6Loopback

	// Deprecated: use github.com/multiformats/go-multiaddr/net
	IP4MappedIP6Loopback = upstream.IP4MappedIP6Loopback
)

var (
	// Deprecated: use github.com/multiformats/go-multiaddr/net
	IP4Unspecified = upstream.IP4Unspecified
	// Deprecated: use github.com/multiformats/go-multiaddr/net
	IP6Unspecified = upstream.IP6Unspecified
)

// Deprecated: use github.com/multiformats/go-multiaddr/net
func IsThinWaist(m ma.Multiaddr) bool {
	return upstream.IsThinWaist(m)
}

// Deprecated: use github.com/multiformats/go-multiaddr/net
func IsIPLoopback(m ma.Multiaddr) bool {
	return upstream.IsIPLoopback(m)
}

// Deprecated: use github.com/multiformats/go-multiaddr/net
func IsIP6LinkLocal(m ma.Multiaddr) bool {
	return upstream.IsIP6LinkLocal(m)
}

// Deprecated: use github.com/multiformats/go-multiaddr/net
func IsIPUnspecified(m ma.Multiaddr) bool {
	return upstream.IsIPUnspecified(m)
}
