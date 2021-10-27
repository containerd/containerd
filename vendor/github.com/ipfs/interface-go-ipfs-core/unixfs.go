package iface

import (
	"context"
	"github.com/ipfs/interface-go-ipfs-core/options"
	path "github.com/ipfs/interface-go-ipfs-core/path"

	"github.com/ipfs/go-cid"
	"github.com/ipfs/go-ipfs-files"
)

type AddEvent struct {
	Name  string
	Path  path.Resolved `json:",omitempty"`
	Bytes int64         `json:",omitempty"`
	Size  string        `json:",omitempty"`
}

// FileType is an enum of possible UnixFS file types.
type FileType int32

const (
	// TUnknown means the file type isn't known (e.g., it hasn't been
	// resolved).
	TUnknown FileType = iota
	// TFile is a regular file.
	TFile
	// TDirectory is a directory.
	TDirectory
	// TSymlink is a symlink.
	TSymlink
)

func (t FileType) String() string {
	switch t {
	case TUnknown:
		return "unknown"
	case TFile:
		return "file"
	case TDirectory:
		return "directory"
	case TSymlink:
		return "symlink"
	default:
		return "<unknown file type>"
	}
}

// DirEntry is a directory entry returned by `Ls`.
type DirEntry struct {
	Name string
	Cid  cid.Cid

	// Only filled when asked to resolve the directory entry.
	Size   uint64   // The size of the file in bytes (or the size of the symlink).
	Type   FileType // The type of the file.
	Target string   // The symlink target (if a symlink).

	Err error
}

// UnixfsAPI is the basic interface to immutable files in IPFS
// NOTE: This API is heavily WIP, things are guaranteed to break frequently
type UnixfsAPI interface {
	// Add imports the data from the reader into merkledag file
	//
	// TODO: a long useful comment on how to use this for many different scenarios
	Add(context.Context, files.Node, ...options.UnixfsAddOption) (path.Resolved, error)

	// Get returns a read-only handle to a file tree referenced by a path
	//
	// Note that some implementations of this API may apply the specified context
	// to operations performed on the returned file
	Get(context.Context, path.Path) (files.Node, error)

	// Ls returns the list of links in a directory. Links aren't guaranteed to be
	// returned in order
	Ls(context.Context, path.Path, ...options.UnixfsLsOption) (<-chan DirEntry, error)
}
