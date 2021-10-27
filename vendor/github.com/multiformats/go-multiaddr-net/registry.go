package manet

import (
	"net"

	ma "github.com/multiformats/go-multiaddr"
	upstream "github.com/multiformats/go-multiaddr/net"
)

// Deprecated: use github.com/multiformats/go-multiaddr/net
type FromNetAddrFunc func(a net.Addr) (ma.Multiaddr, error)

// Deprecated: use github.com/multiformats/go-multiaddr/net
type ToNetAddrFunc func(ma ma.Multiaddr) (net.Addr, error)

// Deprecated: use github.com/multiformats/go-multiaddr/net
type CodecMap = upstream.CodecMap

// Deprecated: use github.com/multiformats/go-multiaddr/net
func NewCodecMap() *CodecMap {
	return upstream.NewCodecMap()
}

// Deprecated: use github.com/multiformats/go-multiaddr/net
type NetCodec = upstream.NetCodec

// Deprecated: use github.com/multiformats/go-multiaddr/net
func RegisterNetCodec(a *NetCodec) {
	upstream.RegisterNetCodec(a)
}
