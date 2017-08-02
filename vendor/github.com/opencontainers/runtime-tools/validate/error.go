package validate

import (
	"errors"
	"fmt"
	"strings"

	rspec "github.com/opencontainers/runtime-spec/specs-go"
)

// ComplianceLevel represents the OCI compliance levels
type ComplianceLevel int

const (
	// MAY-level

	// ComplianceMay represents 'MAY' in RFC2119
	ComplianceMay ComplianceLevel = iota
	// ComplianceOptional represents 'OPTIONAL' in RFC2119
	ComplianceOptional

	// SHOULD-level

	// ComplianceShould represents 'SHOULD' in RFC2119
	ComplianceShould
	// ComplianceShouldNot represents 'SHOULD NOT' in RFC2119
	ComplianceShouldNot
	// ComplianceRecommended represents 'RECOMMENDED' in RFC2119
	ComplianceRecommended
	// ComplianceNotRecommended represents 'NOT RECOMMENDED' in RFC2119
	ComplianceNotRecommended

	// MUST-level

	// ComplianceMust represents 'MUST' in RFC2119
	ComplianceMust
	// ComplianceMustNot represents 'MUST NOT' in RFC2119
	ComplianceMustNot
	// ComplianceShall represents 'SHALL' in RFC2119
	ComplianceShall
	// ComplianceShallNot represents 'SHALL NOT' in RFC2119
	ComplianceShallNot
	// ComplianceRequired represents 'REQUIRED' in RFC2119
	ComplianceRequired
)

// ErrorCode represents the compliance content
type ErrorCode int

const (
	// DefaultFilesystems represents the error code of default filesystems test
	DefaultFilesystems ErrorCode = iota
)

// Error represents an error with compliance level and OCI reference
type Error struct {
	Level     ComplianceLevel
	Reference string
	Err       error
}

const referencePrefix = "https://github.com/opencontainers/runtime-spec/blob"

var ociErrors = map[ErrorCode]Error{
	DefaultFilesystems: Error{Level: ComplianceShould, Reference: "config-linux.md#default-filesystems"},
}

// ParseLevel takes a string level and returns the OCI compliance level constant
func ParseLevel(level string) (ComplianceLevel, error) {
	switch strings.ToUpper(level) {
	case "MAY":
		fallthrough
	case "OPTIONAL":
		return ComplianceMay, nil
	case "SHOULD":
		fallthrough
	case "SHOULDNOT":
		fallthrough
	case "RECOMMENDED":
		fallthrough
	case "NOTRECOMMENDED":
		return ComplianceShould, nil
	case "MUST":
		fallthrough
	case "MUSTNOT":
		fallthrough
	case "SHALL":
		fallthrough
	case "SHALLNOT":
		fallthrough
	case "REQUIRED":
		return ComplianceMust, nil
	}

	var l ComplianceLevel
	return l, fmt.Errorf("%q is not a valid compliance level", level)
}

// NewError creates an Error by ErrorCode and message
func NewError(code ErrorCode, msg string) error {
	err := ociErrors[code]
	err.Err = errors.New(msg)

	return &err
}

// Error returns the error message with OCI reference
func (oci *Error) Error() string {
	return fmt.Sprintf("%s\nRefer to: %s/v%s/%s", oci.Err.Error(), referencePrefix, rspec.Version, oci.Reference)
}
