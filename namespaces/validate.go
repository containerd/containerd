package namespaces

import (
	"regexp"

	"github.com/pkg/errors"
)

const (
	label = `[a-z][a-z0-9]+(?:[-]+[a-z0-9]+)*`
)

func reGroup(s string) string {
	return `(?:` + s + `)`
}

func reAnchor(s string) string {
	return `^` + s + `$`
}

var (
	// namespaceRe validates that a namespace matches valid namespaces.
	//
	// Rules for domains, defined in RFC 1035, section 2.3.1, are used for
	// namespaces.
	namespaceRe = regexp.MustCompile(reAnchor(label + reGroup("[.]"+reGroup(label)) + "*"))

	errNamespaceInvalid = errors.Errorf("invalid namespace, must match %v", namespaceRe)
)

// IsNamespacesValid return true if the error was due to an invalid namespace
// name.
func IsNamespaceInvalid(err error) bool {
	return errors.Cause(err) == errNamespaceInvalid
}

// Validate return nil if the string s is a valid namespace name.
//
// Namespaces must be valid domain names according to RFC 1035, section 2.3.1.
// To enforce case insensitvity, all characters must be lower case.
//
// In general, namespaces that pass this validation, should be safe for use as
// a domain name or filesystem path component.
//
// Typically, this function is used through NamespacesRequired, rather than
// directly.
func Validate(s string) error {
	if !namespaceRe.MatchString(s) {
		return errors.Wrapf(errNamespaceInvalid, "namespace %q", s)
	}
	return nil
}
