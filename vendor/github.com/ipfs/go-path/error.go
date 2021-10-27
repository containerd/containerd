package path

import (
	"fmt"
)

// helper type so path parsing errors include the path
type pathError struct {
	error error
	path  string
}

func (e *pathError) Error() string {
	return fmt.Sprintf("invalid path %q: %s", e.path, e.error)
}

func (e *pathError) Unwrap() error {
	return e.error
}

func (e *pathError) Path() string {
	return e.path
}
