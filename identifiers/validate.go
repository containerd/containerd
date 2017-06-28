// Package identifiers provides common validation for identifiers, keys and ids
// across containerd.
//
// To allow such identifiers to be used across various contexts safely, the character
// set has been restricted to that defined for domains in RFC 1035, section
// 2.3.1. This will make identifiers safe for use across networks, filesystems
// and other media.
//
// While the character set may expand in the future, we guarantee that the
// identifiers will be safe for use as filesystem path components.
package identifiers

import (
	"regexp"

	"github.com/pkg/errors"
)

const (
	label = `[A-Za-z][A-Za-z0-9]+(?:[-]+[A-Za-z0-9]+)*`

	// maxSizeLabel verifies max size limitation for label, defined in
	// RFC 1035, section 2.3.4.
	maxSizeLabel = 63

	// maxSizeName verifies max size limitation for name, defined in
	// RFC 1035, section 2.3.4.
	maxSizeName = 255
)

var (
	// identifierRe validates that a identifier matches valid identifiers.
	//
	// Rules for domains, defined in RFC 1035, section 2.3.1, are used for
	// identifiers.
	identifierRe = regexp.MustCompile(reAnchor(reCapture(label) + reGroup("[.]"+reCapture(label)) + "*"))

	errIdentifierInvalid = errors.Errorf("invalid, must match %v", identifierRe)

	errLengthInvalid = errors.Errorf("invalid, name must less than %d, and label must less than %d.", maxSizeName, maxSizeLabel)
)

// IsInvalid return true if the error was due to an invalid identifer.
func IsInvalid(err error) bool {
	return errors.Cause(err) == errIdentifierInvalid || errors.Cause(err) == errLengthInvalid
}

// Validate return nil if the string s is a valid identifier.
//
// identifiers must be valid domain identifiers according to RFC 1035, section 2.3.1.  To
// enforce case insensitvity, all characters must be lower case.
//
// In general, identifiers that pass this validation, should be safe for use as
// a domain identifier or filesystem path component.
func Validate(s string) error {
	if len(s) > maxSizeName {
		return errors.Wrapf(errLengthInvalid, "identifier %q", s)
	}

	matched := identifierRe.FindStringSubmatch(s)
	if matched == nil {
		return errors.Wrapf(errIdentifierInvalid, "identifier %q", s)
	}

	// to verify length of each matched label.
	for _, l := range matched[1:] {
		if l != "" && len(l) > maxSizeLabel {
			return errors.Wrapf(errLengthInvalid, "identifier %q", s)
		}
	}

	return nil
}

func reGroup(s string) string {
	return `(?:` + s + `)`
}

func reAnchor(s string) string {
	return `^` + s + `$`
}

func reCapture(s string) string {
	return `(` + s + `)`
}
