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

	containerIdLable = `[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}`
)

var (
	// identifierRe validates that a identifier matches valid identifiers.
	//
	// Rules for domains, defined in RFC 1035, section 2.3.1, are used for
	// identifiers.
	identifierRe = regexp.MustCompile(reAnchor(label + reGroup("[.]"+reGroup(label)) + "*"))

	errIdentifierInvalid = errors.Errorf("invalid, must match %v", identifierRe)

	// containerIdRe validates that a identifier matches valid container and task id.
	//
	// TODO(xiekeyang): Current rules for container and task id, following up Docker
	// container name.
	containerIdRe = regexp.MustCompile(reAnchor(containerIdLable))

	errContainerIdInvalid = errors.Errorf("invalid container and task id, must match %v", containerIdRe)
)

// IsInvalid return true if the error was due to an invalid identifer.
func IsInvalid(err error) bool {
	return errors.Cause(err) == errIdentifierInvalid || errors.Cause(err) == errContainerIdInvalid
}

// Validate return nil if the string s is a valid identifier.
//
// identifiers must be valid domain identifiers according to RFC 1035, section 2.3.1.  To
// enforce case insensitvity, all characters must be lower case.
//
// In general, identifiers that pass this validation, should be safe for use as
// a domain identifier or filesystem path component.
func Validate(s string) error {
	if !identifierRe.MatchString(s) {
		return errors.Wrapf(errIdentifierInvalid, "identifier %q", s)
	}
	return nil
}

// ValidateContainerId return nil if the string s is a valid container and task ID.
func ValidateContainerId(s string) error {
	if !containerIdRe.MatchString(s) {
		return errors.Wrapf(errContainerIdInvalid, "identifier %q", s)
	}
	return nil
}

func reGroup(s string) string {
	return `(?:` + s + `)`
}

func reAnchor(s string) string {
	return `^` + s + `$`
}
