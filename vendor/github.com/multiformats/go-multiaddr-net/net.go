// Deprecated: use github.com/multiformats/go-multiaddr/net
package manet

import (
	"net"

	ma "github.com/multiformats/go-multiaddr"
	upstream "github.com/multiformats/go-multiaddr/net"
)

// Deprecated: use github.com/multiformats/go-multiaddr/net
type Conn = upstream.Conn

// Deprecated: use github.com/multiformats/go-multiaddr/net
type PacketConn = upstream.PacketConn

// Deprecated: use github.com/multiformats/go-multiaddr/net
type Dialer = upstream.Dialer

// Deprecated: use github.com/multiformats/go-multiaddr/net
type Listener = upstream.Listener

// Deprecated: use github.com/multiformats/go-multiaddr/net
func WrapNetConn(nconn net.Conn) (Conn, error) {
	return upstream.WrapNetConn(nconn)
}

// Deprecated: use github.com/multiformats/go-multiaddr/net
func Dial(remote ma.Multiaddr) (Conn, error) {
	return upstream.Dial(remote)
}

// Deprecated: use github.com/multiformats/go-multiaddr/net
func NetListener(l Listener) net.Listener {
	return upstream.NetListener(l)
}

// Deprecated: use github.com/multiformats/go-multiaddr/net
func Listen(laddr ma.Multiaddr) (Listener, error) {
	return upstream.Listen(laddr)
}

// Deprecated: use github.com/multiformats/go-multiaddr/net
func WrapNetListener(nl net.Listener) (Listener, error) {
	return upstream.WrapNetListener(nl)
}

// Deprecated: use github.com/multiformats/go-multiaddr/net
func ListenPacket(laddr ma.Multiaddr) (PacketConn, error) {
	return upstream.ListenPacket(laddr)
}

// Deprecated: use github.com/multiformats/go-multiaddr/net
func WrapPacketConn(pc net.PacketConn) (PacketConn, error) {
	return upstream.WrapPacketConn(pc)
}

// Deprecated: use github.com/multiformats/go-multiaddr/net
func InterfaceMultiaddrs() ([]ma.Multiaddr, error) {
	return upstream.InterfaceMultiaddrs()
}

// Deprecated: use github.com/multiformats/go-multiaddr/net
func AddrMatch(match ma.Multiaddr, addrs []ma.Multiaddr) []ma.Multiaddr {
	return upstream.AddrMatch(match, addrs)
}
