package iface

import (
	"context"
	path "github.com/ipfs/interface-go-ipfs-core/path"

	"github.com/ipfs/interface-go-ipfs-core/options"
)

// Pin holds information about pinned resource
type Pin interface {
	// Path to the pinned object
	Path() path.Resolved

	// Type of the pin
	Type() string

	// if not nil, an error happened. Everything else should be ignored.
	Err() error
}

// PinStatus holds information about pin health
type PinStatus interface {
	// Ok indicates whether the pin has been verified to be correct
	Ok() bool

	// BadNodes returns any bad (usually missing) nodes from the pin
	BadNodes() []BadPinNode
}

// BadPinNode is a node that has been marked as bad by Pin.Verify
type BadPinNode interface {
	// Path is the path of the node
	Path() path.Resolved

	// Err is the reason why the node has been marked as bad
	Err() error
}

// PinAPI specifies the interface to pining
type PinAPI interface {
	// Add creates new pin, be default recursive - pinning the whole referenced
	// tree
	Add(context.Context, path.Path, ...options.PinAddOption) error

	// Ls returns list of pinned objects on this node
	Ls(context.Context, ...options.PinLsOption) (<-chan Pin, error)

	// IsPinned returns whether or not the given cid is pinned
	// and an explanation of why its pinned
	IsPinned(context.Context, path.Path, ...options.PinIsPinnedOption) (string, bool, error)

	// Rm removes pin for object specified by the path
	Rm(context.Context, path.Path, ...options.PinRmOption) error

	// Update changes one pin to another, skipping checks for matching paths in
	// the old tree
	Update(ctx context.Context, from path.Path, to path.Path, opts ...options.PinUpdateOption) error

	// Verify verifies the integrity of pinned objects
	Verify(context.Context) (<-chan PinStatus, error)
}
