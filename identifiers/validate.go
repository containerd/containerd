// Package identifiers provides common validation for identifiers, keys and ids
// across containerd.
//
// To allow such identifiers to be used across various contexts safely, the character
// set has been restricted to that defined for domains in RFC 1035, section
// 2.3.1. This will make identifiers safe for use across networks, filesystems
// and other media.
//
// The identifier specification departs from RFC 1035 in that it allows
// "labels" to start with number and only enforces a total length restriction
// of 76 characters.
//
// While the character set may be expanded in the future, identifiers are
// guaranteed to be safely used as filesystem path components.
package identifiers

import (
	"regexp"

	"github.com/containerd/containerd/errdefs"
	"github.com/pkg/errors"
)

const (
	maxLength = 76
	charclass = `[A-Za-z0-9]+`
	label     = charclass + `(:?[-]+` + charclass + `)*`
)

var (
	// identifierRe validates that a identifier matches valid identifiers.
	//
	// Rules for domains, defined in RFC 1035, section 2.3.1, are used for
	// identifiers.
	identifierRe = regexp.MustCompile(reAnchor(label + reGroup("[.]"+reGroup(label)) + "*"))
)

// Validate return nil if the string s is a valid identifier.
//
// identifiers must be valid domain identifiers according to RFC 1035, section 2.3.1.  To
// enforce case insensitvity, all characters must be lower case.
//
// In general, identifiers that pass this validation, should be safe for use as
// a domain identifier or filesystem path component.
func Validate(s string) error {
	if len(s) > maxLength {
		return errors.Wrapf(errdefs.ErrInvalidArgument, "identifier %q greater than maximum length (%d characters)", s, maxLength)
	}

	if !identifierRe.MatchString(s) {
		return errors.Wrapf(errdefs.ErrInvalidArgument, "identifier %q must match %v", s, identifierRe)
	}
	return nil
}

func reGroup(s string) string {
	return `(?:` + s + `)`
}

func reAnchor(s string) string {
	return `^` + s + `$`
}
