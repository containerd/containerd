package path

import (
	"strings"

	cid "github.com/ipfs/go-cid"
	ipfspath "github.com/ipfs/go-path"
)

// Path is a generic wrapper for paths used in the API. A path can be resolved
// to a CID using one of Resolve functions in the API.
//
// Paths must be prefixed with a valid prefix:
//
// * /ipfs - Immutable unixfs path (files)
// * /ipld - Immutable ipld path (data)
// * /ipns - Mutable names. Usually resolves to one of the immutable paths
//TODO: /local (MFS)
type Path interface {
	// String returns the path as a string.
	String() string

	// Namespace returns the first component of the path.
	//
	// For example path "/ipfs/QmHash", calling Namespace() will return "ipfs"
	//
	// Calling this method on invalid paths (IsValid() != nil) will result in
	// empty string
	Namespace() string

	// Mutable returns false if the data pointed to by this path in guaranteed
	// to not change.
	//
	// Note that resolved mutable path can be immutable.
	Mutable() bool

	// IsValid checks if this path is a valid ipfs Path, returning nil iff it is
	// valid
	IsValid() error
}

// Resolved is a path which was resolved to the last resolvable node.
// ResolvedPaths are guaranteed to return nil from `IsValid`
type Resolved interface {
	// Cid returns the CID of the node referenced by the path. Remainder of the
	// path is guaranteed to be within the node.
	//
	// Examples:
	// If you have 3 linked objects: QmRoot -> A -> B:
	//
	// cidB := {"foo": {"bar": 42 }}
	// cidA := {"B": {"/": cidB }}
	// cidRoot := {"A": {"/": cidA }}
	//
	// And resolve paths:
	//
	// * "/ipfs/${cidRoot}"
	//   * Calling Cid() will return `cidRoot`
	//   * Calling Root() will return `cidRoot`
	//   * Calling Remainder() will return ``
	//
	// * "/ipfs/${cidRoot}/A"
	//   * Calling Cid() will return `cidA`
	//   * Calling Root() will return `cidRoot`
	//   * Calling Remainder() will return ``
	//
	// * "/ipfs/${cidRoot}/A/B/foo"
	//   * Calling Cid() will return `cidB`
	//   * Calling Root() will return `cidRoot`
	//   * Calling Remainder() will return `foo`
	//
	// * "/ipfs/${cidRoot}/A/B/foo/bar"
	//   * Calling Cid() will return `cidB`
	//   * Calling Root() will return `cidRoot`
	//   * Calling Remainder() will return `foo/bar`
	Cid() cid.Cid

	// Root returns the CID of the root object of the path
	//
	// Example:
	// If you have 3 linked objects: QmRoot -> A -> B, and resolve path
	// "/ipfs/QmRoot/A/B", the Root method will return the CID of object QmRoot
	//
	// For more examples see the documentation of Cid() method
	Root() cid.Cid

	// Remainder returns unresolved part of the path
	//
	// Example:
	// If you have 2 linked objects: QmRoot -> A, where A is a CBOR node
	// containing the following data:
	//
	// {"foo": {"bar": 42 }}
	//
	// When resolving "/ipld/QmRoot/A/foo/bar", Remainder will return "foo/bar"
	//
	// For more examples see the documentation of Cid() method
	Remainder() string

	Path
}

// path implements coreiface.Path
type path struct {
	path string
}

// resolvedPath implements coreiface.resolvedPath
type resolvedPath struct {
	path
	cid       cid.Cid
	root      cid.Cid
	remainder string
}

// Join appends provided segments to the base path
func Join(base Path, a ...string) Path {
	s := strings.Join(append([]string{base.String()}, a...), "/")
	return &path{path: s}
}

// IpfsPath creates new /ipfs path from the provided CID
func IpfsPath(c cid.Cid) Resolved {
	return &resolvedPath{
		path:      path{"/ipfs/" + c.String()},
		cid:       c,
		root:      c,
		remainder: "",
	}
}

// IpldPath creates new /ipld path from the provided CID
func IpldPath(c cid.Cid) Resolved {
	return &resolvedPath{
		path:      path{"/ipld/" + c.String()},
		cid:       c,
		root:      c,
		remainder: "",
	}
}

// New parses string path to a Path
func New(p string) Path {
	if pp, err := ipfspath.ParsePath(p); err == nil {
		p = pp.String()
	}

	return &path{path: p}
}

// NewResolvedPath creates new Resolved path. This function performs no checks
// and is intended to be used by resolver implementations. Incorrect inputs may
// cause panics. Handle with care.
func NewResolvedPath(ipath ipfspath.Path, c cid.Cid, root cid.Cid, remainder string) Resolved {
	return &resolvedPath{
		path:      path{ipath.String()},
		cid:       c,
		root:      root,
		remainder: remainder,
	}
}

func (p *path) String() string {
	return p.path
}

func (p *path) Namespace() string {
	ip, err := ipfspath.ParsePath(p.path)
	if err != nil {
		return ""
	}

	if len(ip.Segments()) < 1 {
		panic("path without namespace") // this shouldn't happen under any scenario
	}
	return ip.Segments()[0]
}

func (p *path) Mutable() bool {
	// TODO: MFS: check for /local
	return p.Namespace() == "ipns"
}

func (p *path) IsValid() error {
	_, err := ipfspath.ParsePath(p.path)
	return err
}

func (p *resolvedPath) Cid() cid.Cid {
	return p.cid
}

func (p *resolvedPath) Root() cid.Cid {
	return p.root
}

func (p *resolvedPath) Remainder() string {
	return p.remainder
}
