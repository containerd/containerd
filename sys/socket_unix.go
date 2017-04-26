// +build !windows

package sys

import (
	"net"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// CreateUnixSocket creates a unix socket and returns the listener
func CreateUnixSocket(path string) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0660); err != nil {
		return nil, err
	}
	if err := unix.Unlink(path); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return net.Listen("unix", path)
}

// GetLocalListener returns a listerner out of a unix socket.
func GetLocalListener(path string, uid, gid int) (net.Listener, error) {
	l, err := CreateUnixSocket(path)
	if err != nil {
		return l, err
	}

	if err := os.Chmod(path, 0660); err != nil {
		l.Close()
		return nil, err
	}

	if err := os.Chown(path, uid, gid); err != nil {
		l.Close()
		return nil, err
	}

	return l, nil
}
