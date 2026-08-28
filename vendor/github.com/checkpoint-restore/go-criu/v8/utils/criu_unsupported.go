//go:build !linux
// +build !linux

package utils

import "fmt"

func CheckForCriu(version int) error {
	return fmt.Errorf("CRIU not supported on this platform")
}

func IsMemTrack() bool {
	return false
}

func GetCriuVersion() (int, error) {
	return 0, fmt.Errorf("CRIU not supported in this platform")
}
