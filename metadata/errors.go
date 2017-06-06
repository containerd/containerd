package metadata

import "github.com/pkg/errors"

var (
	ErrExists   = errors.New("metadata: exists")
	ErrNotFound = errors.New("metadata: not found")
	ErrNotEmpty = errors.New("metadata: namespace not empty")
)

// IsNotFound returns true if the error is due to a missing image.
func IsNotFound(err error) bool {
	return errors.Cause(err) == ErrNotFound
}

func IsExists(err error) bool {
	return errors.Cause(err) == ErrExists
}

func IsNotEmpty(err error) bool {
	return errors.Cause(err) == ErrNotEmpty
}
