package multiaddr

import (
	"fmt"
	"strings"
)

// These are special sizes
const (
	LengthPrefixedVarSize = -1
)

// Protocol is a Multiaddr protocol description structure.
type Protocol struct {
	// Name is the string representation of the protocol code. E.g., ip4,
	// ip6, tcp, udp, etc.
	Name string

	// Code is the protocol's multicodec (a normal, non-varint number).
	Code int

	// VCode is a precomputed varint encoded version of Code.
	VCode []byte

	// Size is the size of the argument to this protocol.
	//
	// * Size == 0 means this protocol takes no argument.
	// * Size >  0 means this protocol takes a constant sized argument.
	// * Size <  0 means this protocol takes a variable length, varint
	//             prefixed argument.
	Size int // a size of -1 indicates a length-prefixed variable size

	// Path indicates a path protocol (e.g., unix). When parsing multiaddr
	// strings, path protocols consume the remainder of the address instead
	// of stopping at the next forward slash.
	//
	// Size must be LengthPrefixedVarSize.
	Path bool

	// Transcoder converts between the byte representation and the string
	// representation of this protocol's argument (if any).
	//
	// This should only be non-nil if Size != 0
	Transcoder Transcoder
}

var protocolsByName = map[string]Protocol{}
var protocolsByCode = map[int]Protocol{}

// Protocols is the list of multiaddr protocols supported by this module.
var Protocols = []Protocol{}

// SwapToP2pMultiaddrs is a function to make the transition from /ipfs/...
// multiaddrs to /p2p/... multiaddrs easier
// The first stage of the rollout is to ship this package to all users so
// that all users of multiaddr can parse both /ipfs/ and /p2p/ multiaddrs
// as the same code (P_P2P). During this stage of the rollout, all addresses
// with P_P2P will continue printing as /ipfs/, so that older clients without
// the new parsing code won't break.
// Once the network has adopted the new parsing code broadly enough, users of
// multiaddr can add a call to this method to an init function in their codebase.
// This will cause any P_P2P multiaddr to print out as /p2p/ instead of /ipfs/.
// Note that the binary serialization of this multiaddr does not change at any
// point. This means that this code is not a breaking network change at any point
//
// DEPRECATED: this is now the default
func SwapToP2pMultiaddrs() {
}

func AddProtocol(p Protocol) error {
	if _, ok := protocolsByName[p.Name]; ok {
		return fmt.Errorf("protocol by the name %q already exists", p.Name)
	}

	if _, ok := protocolsByCode[p.Code]; ok {
		return fmt.Errorf("protocol code %d already taken by %q", p.Code, p.Code)
	}

	if p.Size != 0 && p.Transcoder == nil {
		return fmt.Errorf("protocols with arguments must define transcoders")
	}
	if p.Path && p.Size >= 0 {
		return fmt.Errorf("path protocols must have variable-length sizes")
	}

	Protocols = append(Protocols, p)
	protocolsByName[p.Name] = p
	protocolsByCode[p.Code] = p
	return nil
}

// ProtocolWithName returns the Protocol description with given string name.
func ProtocolWithName(s string) Protocol {
	return protocolsByName[s]
}

// ProtocolWithCode returns the Protocol description with given protocol code.
func ProtocolWithCode(c int) Protocol {
	return protocolsByCode[c]
}

// ProtocolsWithString returns a slice of protocols matching given string.
func ProtocolsWithString(s string) ([]Protocol, error) {
	s = strings.Trim(s, "/")
	sp := strings.Split(s, "/")
	if len(sp) == 0 {
		return nil, nil
	}

	t := make([]Protocol, len(sp))
	for i, name := range sp {
		p := ProtocolWithName(name)
		if p.Code == 0 {
			return nil, fmt.Errorf("no protocol with name: %s", name)
		}
		t[i] = p
	}
	return t, nil
}
