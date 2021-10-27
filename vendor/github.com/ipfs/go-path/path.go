// Package path contains utilities to work with ipfs paths.
package path

import (
	"fmt"
	"path"
	"strings"

	cid "github.com/ipfs/go-cid"
)

// A Path represents an ipfs content path:
//   * /<cid>/path/to/file
//   * /ipfs/<cid>
//   * /ipns/<cid>/path/to/folder
//   * etc
type Path string

// ^^^
// TODO: debate making this a private struct wrapped in a public interface
// would allow us to control creation, and cache segments.

// FromString safely converts a string type to a Path type.
func FromString(s string) Path {
	return Path(s)
}

// FromCid safely converts a cid.Cid type to a Path type.
func FromCid(c cid.Cid) Path {
	return Path("/ipfs/" + c.String())
}

// Segments returns the different elements of a path
// (elements are delimited by a /).
func (p Path) Segments() []string {
	cleaned := path.Clean(string(p))
	segments := strings.Split(cleaned, "/")

	// Ignore leading slash
	if len(segments[0]) == 0 {
		segments = segments[1:]
	}

	return segments
}

// String converts a path to string.
func (p Path) String() string {
	return string(p)
}

// IsJustAKey returns true if the path is of the form <key> or /ipfs/<key>, or
// /ipld/<key>
func (p Path) IsJustAKey() bool {
	parts := p.Segments()
	return len(parts) == 2 && (parts[0] == "ipfs" || parts[0] == "ipld")
}

// PopLastSegment returns a new Path without its final segment, and the final
// segment, separately. If there is no more to pop (the path is just a key),
// the original path is returned.
func (p Path) PopLastSegment() (Path, string, error) {

	if p.IsJustAKey() {
		return p, "", nil
	}

	segs := p.Segments()
	newPath, err := ParsePath("/" + strings.Join(segs[:len(segs)-1], "/"))
	if err != nil {
		return "", "", err
	}

	return newPath, segs[len(segs)-1], nil
}

// FromSegments returns a path given its different segments.
func FromSegments(prefix string, seg ...string) (Path, error) {
	return ParsePath(prefix + strings.Join(seg, "/"))
}

// ParsePath returns a well-formed ipfs Path.
// The returned path will always be prefixed with /ipfs/ or /ipns/.
// The prefix will be added if not present in the given string.
// This function will return an error when the given string is
// not a valid ipfs path.
func ParsePath(txt string) (Path, error) {
	parts := strings.Split(txt, "/")
	if len(parts) == 1 {
		kp, err := ParseCidToPath(txt)
		if err == nil {
			return kp, nil
		}
	}

	// if the path doesnt begin with a '/'
	// we expect this to start with a hash, and be an 'ipfs' path
	if parts[0] != "" {
		if _, err := decodeCid(parts[0]); err != nil {
			return "", &pathError{error: err, path: txt}
		}
		// The case when the path starts with hash without a protocol prefix
		return Path("/ipfs/" + txt), nil
	}

	if len(parts) < 3 {
		return "", &pathError{error: fmt.Errorf("path does not begin with '/'"), path: txt}
	}

	//TODO: make this smarter
	switch parts[1] {
	case "ipfs", "ipld":
		if parts[2] == "" {
			return "", &pathError{error: fmt.Errorf("not enough path components"), path: txt}
		}
		// Validate Cid.
		_, err := decodeCid(parts[2])
		if err != nil {
			return "", &pathError{error: fmt.Errorf("invalid CID: %s", err), path: txt}
		}
	case "ipns":
		if parts[2] == "" {
			return "", &pathError{error: fmt.Errorf("not enough path components"), path: txt}
		}
	default:
		return "", &pathError{error: fmt.Errorf("unknown namespace %q", parts[1]), path: txt}
	}

	return Path(txt), nil
}

// ParseCidToPath takes a CID in string form and returns a valid ipfs Path.
func ParseCidToPath(txt string) (Path, error) {
	if txt == "" {
		return "", &pathError{error: fmt.Errorf("empty"), path: txt}
	}

	c, err := decodeCid(txt)
	if err != nil {
		return "", &pathError{error: err, path: txt}
	}

	return FromCid(c), nil
}

// IsValid checks if a path is a valid ipfs Path.
func (p *Path) IsValid() error {
	_, err := ParsePath(p.String())
	return err
}

// Join joins strings slices using /
func Join(pths []string) string {
	return strings.Join(pths, "/")
}

// SplitList splits strings usings /
func SplitList(pth string) []string {
	return strings.Split(pth, "/")
}

// SplitAbsPath clean up and split fpath. It extracts the first component (which
// must be a Multihash) and return it separately.
func SplitAbsPath(fpath Path) (cid.Cid, []string, error) {
	parts := fpath.Segments()
	if parts[0] == "ipfs" || parts[0] == "ipld" {
		parts = parts[1:]
	}

	// if nothing, bail.
	if len(parts) == 0 {
		return cid.Cid{}, nil, &pathError{error: fmt.Errorf("empty"), path: string(fpath)}
	}

	c, err := decodeCid(parts[0])
	// first element in the path is a cid
	if err != nil {
		return cid.Cid{}, nil, &pathError{error: fmt.Errorf("invalid CID: %s", err), path: string(fpath)}
	}

	return c, parts[1:], nil
}

func decodeCid(cstr string) (cid.Cid, error) {
	c, err := cid.Decode(cstr)
	if err != nil && len(cstr) == 46 && cstr[:2] == "qm" { // https://github.com/ipfs/go-ipfs/issues/7792
		return cid.Cid{}, fmt.Errorf("%v (possible lowercased CIDv0; consider converting to a case-agnostic CIDv1, such as base32)", err)
	}
	return c, err
}
