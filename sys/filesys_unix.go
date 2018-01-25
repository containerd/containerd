// +build !windows

package sys

import "os"

// ForceRemoveAll on unix is just a wrapper for os.RemoveAll
func ForceRemoveAll(path string) error {
	return os.RemoveAll(path)
}
