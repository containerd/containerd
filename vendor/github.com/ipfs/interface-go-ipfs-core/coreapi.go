// Package iface defines IPFS Core API which is a set of interfaces used to
// interact with IPFS nodes.
package iface

import (
	"context"

	path "github.com/ipfs/interface-go-ipfs-core/path"

	"github.com/ipfs/interface-go-ipfs-core/options"

	ipld "github.com/ipfs/go-ipld-format"
)

// CoreAPI defines an unified interface to IPFS for Go programs
type CoreAPI interface {
	// Unixfs returns an implementation of Unixfs API
	Unixfs() UnixfsAPI

	// Block returns an implementation of Block API
	Block() BlockAPI

	// Dag returns an implementation of Dag API
	Dag() APIDagService

	// Name returns an implementation of Name API
	Name() NameAPI

	// Key returns an implementation of Key API
	Key() KeyAPI

	// Pin returns an implementation of Pin API
	Pin() PinAPI

	// ObjectAPI returns an implementation of Object API
	Object() ObjectAPI

	// Dht returns an implementation of Dht API
	Dht() DhtAPI

	// Swarm returns an implementation of Swarm API
	Swarm() SwarmAPI

	// PubSub returns an implementation of PubSub API
	PubSub() PubSubAPI

	// ResolvePath resolves the path using Unixfs resolver
	ResolvePath(context.Context, path.Path) (path.Resolved, error)

	// ResolveNode resolves the path (if not resolved already) using Unixfs
	// resolver, gets and returns the resolved Node
	ResolveNode(context.Context, path.Path) (ipld.Node, error)

	// WithOptions creates new instance of CoreAPI based on this instance with
	// a set of options applied
	WithOptions(...options.ApiOption) (CoreAPI, error)
}
