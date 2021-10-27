package iface

import (
	"context"
	path "github.com/ipfs/interface-go-ipfs-core/path"

	"github.com/ipfs/interface-go-ipfs-core/options"

	"github.com/libp2p/go-libp2p-core/peer"
)

// Key specifies the interface to Keys in KeyAPI Keystore
type Key interface {
	// Key returns key name
	Name() string

	// Path returns key path
	Path() path.Path

	// ID returns key PeerID
	ID() peer.ID
}

// KeyAPI specifies the interface to Keystore
type KeyAPI interface {
	// Generate generates new key, stores it in the keystore under the specified
	// name and returns a base58 encoded multihash of it's public key
	Generate(ctx context.Context, name string, opts ...options.KeyGenerateOption) (Key, error)

	// Rename renames oldName key to newName. Returns the key and whether another
	// key was overwritten, or an error
	Rename(ctx context.Context, oldName string, newName string, opts ...options.KeyRenameOption) (Key, bool, error)

	// List lists keys stored in keystore
	List(ctx context.Context) ([]Key, error)

	// Self returns the 'main' node key
	Self(ctx context.Context) (Key, error)

	// Remove removes keys from keystore. Returns ipns path of the removed key
	Remove(ctx context.Context, name string) (Key, error)
}
