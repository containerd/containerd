package manet

import (
	"fmt"
	"net"
	"sync"

	ma "github.com/multiformats/go-multiaddr"
)

// FromNetAddrFunc is a generic function which converts a net.Addr to Multiaddress
type FromNetAddrFunc func(a net.Addr) (ma.Multiaddr, error)

// ToNetAddrFunc is a generic function which converts a Multiaddress to net.Addr
type ToNetAddrFunc func(ma ma.Multiaddr) (net.Addr, error)

var defaultCodecs = NewCodecMap()

func init() {
	defaultCodecs.RegisterFromNetAddr(parseTCPNetAddr, "tcp", "tcp4", "tcp6")
	defaultCodecs.RegisterFromNetAddr(parseUDPNetAddr, "udp", "udp4", "udp6")
	defaultCodecs.RegisterFromNetAddr(parseIPNetAddr, "ip", "ip4", "ip6")
	defaultCodecs.RegisterFromNetAddr(parseIPPlusNetAddr, "ip+net")
	defaultCodecs.RegisterFromNetAddr(parseUnixNetAddr, "unix")

	defaultCodecs.RegisterToNetAddr(parseBasicNetMaddr, "tcp", "udp", "ip6", "ip4", "unix")
}

// CodecMap holds a map of NetCodecs indexed by their Protocol ID
// along with parsers for the addresses they use.
// It is used to keep a list of supported network address codecs (protocols
// which addresses can be converted to and from multiaddresses).
type CodecMap struct {
	codecs       map[string]*NetCodec
	addrParsers  map[string]FromNetAddrFunc
	maddrParsers map[string]ToNetAddrFunc
	lk           sync.Mutex
}

// NewCodecMap initializes and returns a CodecMap object.
func NewCodecMap() *CodecMap {
	return &CodecMap{
		addrParsers:  make(map[string]FromNetAddrFunc),
		maddrParsers: make(map[string]ToNetAddrFunc),
	}
}

// NetCodec is used to identify a network codec, that is, a network type for
// which we are able to translate multiaddresses into standard Go net.Addr
// and back.
//
// Deprecated: Unfortunately, these mappings aren't one to one. This abstraction
// assumes that multiple "networks" can map to a single multiaddr protocol but
// not the reverse. For example, this abstraction supports `tcp6, tcp4, tcp ->
// /tcp/` really well but doesn't support `ip -> {/ip4/, /ip6/}`.
//
// Please use `RegisterFromNetAddr` and `RegisterToNetAddr` directly.
type NetCodec struct {
	// NetAddrNetworks is an array of strings that may be returned
	// by net.Addr.Network() calls on addresses belonging to this type
	NetAddrNetworks []string

	// ProtocolName is the string value for Multiaddr address keys
	ProtocolName string

	// ParseNetAddr parses a net.Addr belonging to this type into a multiaddr
	ParseNetAddr FromNetAddrFunc

	// ConvertMultiaddr converts a multiaddr of this type back into a net.Addr
	ConvertMultiaddr ToNetAddrFunc

	// Protocol returns the multiaddr protocol struct for this type
	Protocol ma.Protocol
}

// RegisterNetCodec adds a new NetCodec to the default codecs.
func RegisterNetCodec(a *NetCodec) {
	defaultCodecs.RegisterNetCodec(a)
}

// RegisterNetCodec adds a new NetCodec to the CodecMap. This function is
// thread safe.
func (cm *CodecMap) RegisterNetCodec(a *NetCodec) {
	cm.lk.Lock()
	defer cm.lk.Unlock()
	for _, n := range a.NetAddrNetworks {
		cm.addrParsers[n] = a.ParseNetAddr
	}

	cm.maddrParsers[a.ProtocolName] = a.ConvertMultiaddr
}

// RegisterFromNetAddr registers a conversion from net.Addr instances to multiaddrs
func (cm *CodecMap) RegisterFromNetAddr(from FromNetAddrFunc, networks ...string) {
	cm.lk.Lock()
	defer cm.lk.Unlock()

	for _, n := range networks {
		cm.addrParsers[n] = from
	}
}

// RegisterToNetAddr registers a conversion from multiaddrs to net.Addr instances
func (cm *CodecMap) RegisterToNetAddr(to ToNetAddrFunc, protocols ...string) {
	cm.lk.Lock()
	defer cm.lk.Unlock()

	for _, p := range protocols {
		cm.maddrParsers[p] = to
	}
}

func (cm *CodecMap) getAddrParser(net string) (FromNetAddrFunc, error) {
	cm.lk.Lock()
	defer cm.lk.Unlock()

	parser, ok := cm.addrParsers[net]
	if !ok {
		return nil, fmt.Errorf("unknown network %v", net)
	}
	return parser, nil
}

func (cm *CodecMap) getMaddrParser(name string) (ToNetAddrFunc, error) {
	cm.lk.Lock()
	defer cm.lk.Unlock()
	p, ok := cm.maddrParsers[name]
	if !ok {
		return nil, fmt.Errorf("network not supported: %s", name)
	}

	return p, nil
}
