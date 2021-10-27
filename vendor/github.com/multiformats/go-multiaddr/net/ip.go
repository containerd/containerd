package manet

import (
	"net"

	ma "github.com/multiformats/go-multiaddr"
)

// Loopback Addresses
var (
	// IP4Loopback is the ip4 loopback multiaddr
	IP4Loopback = ma.StringCast("/ip4/127.0.0.1")

	// IP6Loopback is the ip6 loopback multiaddr
	IP6Loopback = ma.StringCast("/ip6/::1")

	// IP4MappedIP6Loopback is the IPv4 Mapped IPv6 loopback address.
	IP4MappedIP6Loopback = ma.StringCast("/ip6/::ffff:127.0.0.1")
)

// Unspecified Addresses (used for )
var (
	IP4Unspecified = ma.StringCast("/ip4/0.0.0.0")
	IP6Unspecified = ma.StringCast("/ip6/::")
)

// IsThinWaist returns whether a Multiaddr starts with "Thin Waist" Protocols.
// This means: /{IP4, IP6}[/{TCP, UDP}]
func IsThinWaist(m ma.Multiaddr) bool {
	m = zoneless(m)
	if m == nil {
		return false
	}
	p := m.Protocols()

	// nothing? not even a waist.
	if len(p) == 0 {
		return false
	}

	if p[0].Code != ma.P_IP4 && p[0].Code != ma.P_IP6 {
		return false
	}

	// only IP? still counts.
	if len(p) == 1 {
		return true
	}

	switch p[1].Code {
	case ma.P_TCP, ma.P_UDP, ma.P_IP4, ma.P_IP6:
		return true
	default:
		return false
	}
}

// IsIPLoopback returns whether a Multiaddr starts with a "Loopback" IP address
// This means either /ip4/127.*.*.*/*, /ip6/::1/*, or /ip6/::ffff:127.*.*.*.*/*,
// or /ip6zone/<any value>/ip6/<one of the preceding ip6 values>/*
func IsIPLoopback(m ma.Multiaddr) bool {
	m = zoneless(m)
	c, _ := ma.SplitFirst(m)
	if c == nil {
		return false
	}
	switch c.Protocol().Code {
	case ma.P_IP4, ma.P_IP6:
		return net.IP(c.RawValue()).IsLoopback()
	}
	return false
}

// IsIP6LinkLocal returns whether a Multiaddr starts with an IPv6 link-local
// multiaddress (with zero or one leading zone). These addresses are non
// routable.
func IsIP6LinkLocal(m ma.Multiaddr) bool {
	m = zoneless(m)
	c, _ := ma.SplitFirst(m)
	if c == nil || c.Protocol().Code != ma.P_IP6 {
		return false
	}
	ip := net.IP(c.RawValue())
	return ip.IsLinkLocalMulticast() || ip.IsLinkLocalUnicast()
}

// IsIPUnspecified returns whether a Multiaddr starts with an Unspecified IP address
// This means either /ip4/0.0.0.0/* or /ip6/::/*
func IsIPUnspecified(m ma.Multiaddr) bool {
	m = zoneless(m)
	if m == nil {
		return false
	}
	c, _ := ma.SplitFirst(m)
	return net.IP(c.RawValue()).IsUnspecified()
}

// If m matches [zone,ip6,...], return [ip6,...]
// else if m matches [], [zone], or [zone,...], return nil
// else return m
func zoneless(m ma.Multiaddr) ma.Multiaddr {
	head, tail := ma.SplitFirst(m)
	if head == nil {
		return nil
	}
	if head.Protocol().Code == ma.P_IP6ZONE {
		if tail == nil {
			return nil
		}
		tailhead, _ := ma.SplitFirst(tail)
		if tailhead.Protocol().Code != ma.P_IP6 {
			return nil
		}
		return tail
	} else {
		return m
	}
}
