// +build darwin

package oci

import (
	"errors"
	"os"
)

func unlockpt(f *os.File) error {
	return errors.New("oci.unlockpt is not supported on osx")
}

func ptsname(f *os.File) (string, error) {
	return "", errors.New("oci.unlockpt is not supported on osx")
}
