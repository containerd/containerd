// +build darwin

package sys

import (
	"errors"
)

func Mount(source string, target string, fstype string, options []string) (err error) {
	return errors.New("sys.Mount is not supported on osx")
}
