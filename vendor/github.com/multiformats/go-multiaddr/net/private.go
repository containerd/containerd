package manet

import (
	"net"

	ma "github.com/multiformats/go-multiaddr"
)

// Private4 and Private6 are well-known private networks
var Private4, Private6 []*net.IPNet
var privateCIDR4 = []string{
	// localhost
	"127.0.0.0/8",
	// private networks
	"10.0.0.0/8",
	"100.64.0.0/10",
	"172.16.0.0/12",
	"192.168.0.0/16",
	// link local
	"169.254.0.0/16",
}
var privateCIDR6 = []string{
	// localhost
	"::1/128",
	// ULA reserved
	"fc00::/7",
	// link local
	"fe80::/10",
}

// Unroutable4 and Unroutable6 are well known unroutable address ranges
var Unroutable4, Unroutable6 []*net.IPNet
var unroutableCIDR4 = []string{
	"0.0.0.0/8",
	"192.0.0.0/26",
	"192.0.2.0/24",
	"192.88.99.0/24",
	"198.18.0.0/15",
	"198.51.100.0/24",
	"203.0.113.0/24",
	"224.0.0.0/4",
	"240.0.0.0/4",
	"255.255.255.255/32",
}
var unroutableCIDR6 = []string{
	"ff00::/8",
}

func init() {
	Private4 = parseCIDR(privateCIDR4)
	Private6 = parseCIDR(privateCIDR6)
	Unroutable4 = parseCIDR(unroutableCIDR4)
	Unroutable6 = parseCIDR(unroutableCIDR6)
}

func parseCIDR(cidrs []string) []*net.IPNet {
	ipnets := make([]*net.IPNet, len(cidrs))
	for i, cidr := range cidrs {
		_, ipnet, err := net.ParseCIDR(cidr)
		if err != nil {
			panic(err)
		}
		ipnets[i] = ipnet
	}
	return ipnets
}

// IsPublicAddr retruns true if the IP part of the multiaddr is a publicly routable address
func IsPublicAddr(a ma.Multiaddr) bool {
	isPublic := false
	ma.ForEach(a, func(c ma.Component) bool {
		switch c.Protocol().Code {
		case ma.P_IP6ZONE:
			return true
		default:
			return false
		case ma.P_IP4:
			ip := net.IP(c.RawValue())
			isPublic = !inAddrRange(ip, Private4) && !inAddrRange(ip, Unroutable4)
		case ma.P_IP6:
			ip := net.IP(c.RawValue())
			isPublic = !inAddrRange(ip, Private6) && !inAddrRange(ip, Unroutable6)
		}
		return false
	})
	return isPublic
}

// IsPrivateAddr returns true if the IP part of the mutiaddr is in a private network
func IsPrivateAddr(a ma.Multiaddr) bool {
	isPrivate := false
	ma.ForEach(a, func(c ma.Component) bool {
		switch c.Protocol().Code {
		case ma.P_IP6ZONE:
			return true
		default:
			return false
		case ma.P_IP4:
			isPrivate = inAddrRange(net.IP(c.RawValue()), Private4)
		case ma.P_IP6:
			isPrivate = inAddrRange(net.IP(c.RawValue()), Private6)
		}
		return false
	})
	return isPrivate
}

func inAddrRange(ip net.IP, ipnets []*net.IPNet) bool {
	for _, ipnet := range ipnets {
		if ipnet.Contains(ip) {
			return true
		}
	}

	return false
}
