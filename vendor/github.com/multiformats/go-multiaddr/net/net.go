// Package manet provides Multiaddr
// (https://github.com/multiformats/go-multiaddr) specific versions of common
// functions in Go's standard `net` package. This means wrappers of standard
// net symbols like `net.Dial` and `net.Listen`, as well as conversion to
// and from `net.Addr`.
package manet

import (
	"context"
	"fmt"
	"net"

	ma "github.com/multiformats/go-multiaddr"
)

// Conn is the equivalent of a net.Conn object. It is the
// result of calling the Dial or Listen functions in this
// package, with associated local and remote Multiaddrs.
type Conn interface {
	net.Conn

	// LocalMultiaddr returns the local Multiaddr associated
	// with this connection
	LocalMultiaddr() ma.Multiaddr

	// RemoteMultiaddr returns the remote Multiaddr associated
	// with this connection
	RemoteMultiaddr() ma.Multiaddr
}

type halfOpen interface {
	net.Conn
	CloseRead() error
	CloseWrite() error
}

func wrap(nconn net.Conn, laddr, raddr ma.Multiaddr) Conn {
	endpts := maEndpoints{
		laddr: laddr,
		raddr: raddr,
	}
	// This sucks. However, it's the only way to reliably expose the
	// underlying methods. This way, users that need access to, e.g.,
	// CloseRead and CloseWrite, can do so via type assertions.
	switch nconn := nconn.(type) {
	case *net.TCPConn:
		return &struct {
			*net.TCPConn
			maEndpoints
		}{nconn, endpts}
	case *net.UDPConn:
		return &struct {
			*net.UDPConn
			maEndpoints
		}{nconn, endpts}
	case *net.IPConn:
		return &struct {
			*net.IPConn
			maEndpoints
		}{nconn, endpts}
	case *net.UnixConn:
		return &struct {
			*net.UnixConn
			maEndpoints
		}{nconn, endpts}
	case halfOpen:
		return &struct {
			halfOpen
			maEndpoints
		}{nconn, endpts}
	default:
		return &struct {
			net.Conn
			maEndpoints
		}{nconn, endpts}
	}
}

// WrapNetConn wraps a net.Conn object with a Multiaddr friendly Conn.
//
// This function does it's best to avoid "hiding" methods exposed by the wrapped
// type. Guarantees:
//
// * If the wrapped connection exposes the "half-open" closer methods
//   (CloseWrite, CloseRead), these will be available on the wrapped connection
//   via type assertions.
// * If the wrapped connection is a UnixConn, IPConn, TCPConn, or UDPConn, all
//   methods on these wrapped connections will be available via type assertions.
func WrapNetConn(nconn net.Conn) (Conn, error) {
	if nconn == nil {
		return nil, fmt.Errorf("failed to convert nconn.LocalAddr: nil")
	}

	laddr, err := FromNetAddr(nconn.LocalAddr())
	if err != nil {
		return nil, fmt.Errorf("failed to convert nconn.LocalAddr: %s", err)
	}

	raddr, err := FromNetAddr(nconn.RemoteAddr())
	if err != nil {
		return nil, fmt.Errorf("failed to convert nconn.RemoteAddr: %s", err)
	}

	return wrap(nconn, laddr, raddr), nil
}

type maEndpoints struct {
	laddr ma.Multiaddr
	raddr ma.Multiaddr
}

// LocalMultiaddr returns the local address associated with
// this connection
func (c *maEndpoints) LocalMultiaddr() ma.Multiaddr {
	return c.laddr
}

// RemoteMultiaddr returns the remote address associated with
// this connection
func (c *maEndpoints) RemoteMultiaddr() ma.Multiaddr {
	return c.raddr
}

// Dialer contains options for connecting to an address. It
// is effectively the same as net.Dialer, but its LocalAddr
// and RemoteAddr options are Multiaddrs, instead of net.Addrs.
type Dialer struct {

	// Dialer is just an embedded net.Dialer, with all its options.
	net.Dialer

	// LocalAddr is the local address to use when dialing an
	// address. The address must be of a compatible type for the
	// network being dialed.
	// If nil, a local address is automatically chosen.
	LocalAddr ma.Multiaddr
}

// Dial connects to a remote address, using the options of the
// Dialer. Dialer uses an underlying net.Dialer to Dial a
// net.Conn, then wraps that in a Conn object (with local and
// remote Multiaddrs).
func (d *Dialer) Dial(remote ma.Multiaddr) (Conn, error) {
	return d.DialContext(context.Background(), remote)
}

// DialContext allows to provide a custom context to Dial().
func (d *Dialer) DialContext(ctx context.Context, remote ma.Multiaddr) (Conn, error) {
	// if a LocalAddr is specified, use it on the embedded dialer.
	if d.LocalAddr != nil {
		// convert our multiaddr to net.Addr friendly
		naddr, err := ToNetAddr(d.LocalAddr)
		if err != nil {
			return nil, err
		}

		// set the dialer's LocalAddr as naddr
		d.Dialer.LocalAddr = naddr
	}

	// get the net.Dial friendly arguments from the remote addr
	rnet, rnaddr, err := DialArgs(remote)
	if err != nil {
		return nil, err
	}

	// ok, Dial!
	var nconn net.Conn
	switch rnet {
	case "tcp", "tcp4", "tcp6", "udp", "udp4", "udp6", "unix":
		nconn, err = d.Dialer.DialContext(ctx, rnet, rnaddr)
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unrecognized network: %s", rnet)
	}

	// get local address (pre-specified or assigned within net.Conn)
	local := d.LocalAddr
	// This block helps us avoid parsing addresses in transports (such as unix
	// sockets) that don't have local addresses when dialing out.
	if local == nil && nconn.LocalAddr().String() != "" {
		local, err = FromNetAddr(nconn.LocalAddr())
		if err != nil {
			return nil, err
		}
	}
	return wrap(nconn, local, remote), nil
}

// Dial connects to a remote address. It uses an underlying net.Conn,
// then wraps it in a Conn object (with local and remote Multiaddrs).
func Dial(remote ma.Multiaddr) (Conn, error) {
	return (&Dialer{}).Dial(remote)
}

// A Listener is a generic network listener for stream-oriented protocols.
// it uses an embedded net.Listener, overriding net.Listener.Accept to
// return a Conn and providing Multiaddr.
type Listener interface {
	// Accept waits for and returns the next connection to the listener.
	// Returns a Multiaddr friendly Conn
	Accept() (Conn, error)

	// Close closes the listener.
	// Any blocked Accept operations will be unblocked and return errors.
	Close() error

	// Multiaddr returns the listener's (local) Multiaddr.
	Multiaddr() ma.Multiaddr

	// Addr returns the net.Listener's network address.
	Addr() net.Addr
}

type netListenerAdapter struct {
	Listener
}

func (nla *netListenerAdapter) Accept() (net.Conn, error) {
	return nla.Listener.Accept()
}

// NetListener turns this Listener into a net.Listener.
//
// * Connections returned from Accept implement multiaddr/net Conn.
// * Calling WrapNetListener on the net.Listener returned by this function will
//   return the original (underlying) multiaddr/net Listener.
func NetListener(l Listener) net.Listener {
	return &netListenerAdapter{l}
}

// maListener implements Listener
type maListener struct {
	net.Listener
	laddr ma.Multiaddr
}

// Accept waits for and returns the next connection to the listener.
// Returns a Multiaddr friendly Conn
func (l *maListener) Accept() (Conn, error) {
	nconn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}

	var raddr ma.Multiaddr
	// This block protects us in transports (i.e. unix sockets) that don't have
	// remote addresses for inbound connections.
	if nconn.RemoteAddr().String() != "" {
		raddr, err = FromNetAddr(nconn.RemoteAddr())
		if err != nil {
			return nil, fmt.Errorf("failed to convert conn.RemoteAddr: %s", err)
		}
	}

	return wrap(nconn, l.laddr, raddr), nil
}

// Multiaddr returns the listener's (local) Multiaddr.
func (l *maListener) Multiaddr() ma.Multiaddr {
	return l.laddr
}

// Addr returns the listener's network address.
func (l *maListener) Addr() net.Addr {
	return l.Listener.Addr()
}

// Listen announces on the local network address laddr.
// The Multiaddr must be a "ThinWaist" stream-oriented network:
// ip4/tcp, ip6/tcp, (TODO: unix, unixpacket)
// See Dial for the syntax of laddr.
func Listen(laddr ma.Multiaddr) (Listener, error) {

	// get the net.Listen friendly arguments from the remote addr
	lnet, lnaddr, err := DialArgs(laddr)
	if err != nil {
		return nil, err
	}

	nl, err := net.Listen(lnet, lnaddr)
	if err != nil {
		return nil, err
	}

	// we want to fetch the new multiaddr from the listener, as it may
	// have resolved to some other value. WrapNetListener does it for us.
	return WrapNetListener(nl)
}

// WrapNetListener wraps a net.Listener with a manet.Listener.
func WrapNetListener(nl net.Listener) (Listener, error) {
	if nla, ok := nl.(*netListenerAdapter); ok {
		return nla.Listener, nil
	}

	laddr, err := FromNetAddr(nl.Addr())
	if err != nil {
		return nil, err
	}

	return &maListener{
		Listener: nl,
		laddr:    laddr,
	}, nil
}

// A PacketConn is a generic packet oriented network connection which uses an
// underlying net.PacketConn, wrapped with the locally bound Multiaddr.
type PacketConn interface {
	net.PacketConn

	LocalMultiaddr() ma.Multiaddr

	ReadFromMultiaddr(b []byte) (int, ma.Multiaddr, error)
	WriteToMultiaddr(b []byte, maddr ma.Multiaddr) (int, error)
}

// maPacketConn implements PacketConn
type maPacketConn struct {
	net.PacketConn
	laddr ma.Multiaddr
}

var _ PacketConn = (*maPacketConn)(nil)

// LocalMultiaddr returns the bound local Multiaddr.
func (l *maPacketConn) LocalMultiaddr() ma.Multiaddr {
	return l.laddr
}

func (l *maPacketConn) ReadFromMultiaddr(b []byte) (int, ma.Multiaddr, error) {
	n, addr, err := l.ReadFrom(b)
	maddr, _ := FromNetAddr(addr)
	return n, maddr, err
}

func (l *maPacketConn) WriteToMultiaddr(b []byte, maddr ma.Multiaddr) (int, error) {
	addr, err := ToNetAddr(maddr)
	if err != nil {
		return 0, err
	}
	return l.WriteTo(b, addr)
}

// ListenPacket announces on the local network address laddr.
// The Multiaddr must be a packet driven network, like udp4 or udp6.
// See Dial for the syntax of laddr.
func ListenPacket(laddr ma.Multiaddr) (PacketConn, error) {
	lnet, lnaddr, err := DialArgs(laddr)
	if err != nil {
		return nil, err
	}

	pc, err := net.ListenPacket(lnet, lnaddr)
	if err != nil {
		return nil, err
	}

	// We want to fetch the new multiaddr from the listener, as it may
	// have resolved to some other value. WrapPacketConn does this.
	return WrapPacketConn(pc)
}

// WrapPacketConn wraps a net.PacketConn with a manet.PacketConn.
func WrapPacketConn(pc net.PacketConn) (PacketConn, error) {
	laddr, err := FromNetAddr(pc.LocalAddr())
	if err != nil {
		return nil, err
	}

	return &maPacketConn{
		PacketConn: pc,
		laddr:      laddr,
	}, nil
}

// InterfaceMultiaddrs will return the addresses matching net.InterfaceAddrs
func InterfaceMultiaddrs() ([]ma.Multiaddr, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}

	maddrs := make([]ma.Multiaddr, len(addrs))
	for i, a := range addrs {
		maddrs[i], err = FromNetAddr(a)
		if err != nil {
			return nil, err
		}
	}
	return maddrs, nil
}

// AddrMatch returns the Multiaddrs that match the protocol stack on addr
func AddrMatch(match ma.Multiaddr, addrs []ma.Multiaddr) []ma.Multiaddr {

	// we should match transports entirely.
	p1s := match.Protocols()

	out := make([]ma.Multiaddr, 0, len(addrs))
	for _, a := range addrs {
		p2s := a.Protocols()
		if len(p1s) != len(p2s) {
			continue
		}

		match := true
		for i, p2 := range p2s {
			if p1s[i].Code != p2.Code {
				match = false
				break
			}
		}
		if match {
			out = append(out, a)
		}
	}
	return out
}
