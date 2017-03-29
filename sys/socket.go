package sys

import (
	"net"
	"os"
	"path/filepath"
	"syscall"
)

// CreateUnixSocket creates a unix socket and returns the listener
func CreateUnixSocket(path string) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0660); err != nil {
		return nil, err
	}
	if err := syscall.Unlink(path); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return net.Listen("unix", path)
}
