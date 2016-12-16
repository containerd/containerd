// +build darwin

package oci

import (
	"errors"
)

func GetSubreaper() (int, error) {
	return -1, errors.New("oc.GetSubreaper is not supported on osx")
}

func SetSubreaper(i int) error {
	return errors.New("oc.GetSubreaper is not supported on osx")
}
