//go:build !windows

/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package sys

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// CreateUnixSocket creates a unix socket and returns the listener
func CreateUnixSocket(path string) (net.Listener, error) {
	// BSDs have a 104 limit
	if len(path) > 104 {
		return nil, fmt.Errorf("%q: unix socket path too long (> 104)", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0660); err != nil {
		return nil, err
	}
	if err := unix.Unlink(path); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return net.Listen("unix", path)
}

// GetLocalListener returns a listener out of a unix socket.
func GetLocalListener(path string, uid, gid int) (net.Listener, error) {
	// Ensure parent directory is created
	if err := mkdirAs(filepath.Dir(path), uid, gid); err != nil {
		return nil, err
	}

	l, err := CreateUnixSocket(path)
	if err != nil {
		return l, fmt.Errorf("failed to create unix socket on %s: %w", path, err)
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

func mkdirAs(path string, uid, gid int) error {
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		if errors.Is(err, os.ErrPermission) {
			return wrapSocketDirPermissionErr(err)
		}
		return err
	}

	if err := os.MkdirAll(path, 0770); err != nil {
		if errors.Is(err, os.ErrPermission) {
			return wrapSocketDirPermissionErr(err)
		}
		return err
	}

	return os.Chown(path, uid, gid)
}

// wrapSocketDirPermissionErr adds a hint to a permission-denied error
// encountered while stat-ing or creating a socket directory. This directory
// is derived from a plugin's configured socket address, and the grpc and
// ttrpc server plugins carry their own hardcoded default for that address,
// so a user who only overrides one of root/state/[grpc] address can still
// hit this on the others (including the debug plugin, if its address is
// configured to a similarly unwritable path).
func wrapSocketDirPermissionErr(err error) error {
	return fmt.Errorf("%w (the configured socket address points at a directory that is not writable/accessible by the current user; if running containerd as a non-root user, configure a writable address for the grpc, ttrpc, and debug plugins, whose addresses may otherwise default to or be set to a directory that requires root)", err)
}
