package manet

import (
	"net"

	ma "github.com/multiformats/go-multiaddr"
	upstream "github.com/multiformats/go-multiaddr/net"
)

// Deprecated: use github.com/multiformats/go-multiaddr/net
func FromNetAddr(a net.Addr) (ma.Multiaddr, error) {
	return upstream.FromNetAddr(a)
}

// Deprecated: use github.com/multiformats/go-multiaddr/net
func ToNetAddr(maddr ma.Multiaddr) (net.Addr, error) {
	return upstream.ToNetAddr(maddr)
}

// Deprecated: use github.com/multiformats/go-multiaddr/net
func FromIPAndZone(ip net.IP, zone string) (ma.Multiaddr, error) {
	return upstream.FromIPAndZone(ip, zone)
}

// Deprecated: use github.com/multiformats/go-multiaddr/net
func FromIP(ip net.IP) (ma.Multiaddr, error) {
	return upstream.FromIP(ip)
}

// Deprecated: use github.com/multiformats/go-multiaddr/net
func ToIP(addr ma.Multiaddr) (net.IP, error) {
	return upstream.ToIP(addr)
}

// Deprecated: use github.com/multiformats/go-multiaddr/net
func DialArgs(m ma.Multiaddr) (string, string, error) {
	return upstream.DialArgs(m)
}
