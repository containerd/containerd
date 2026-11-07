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
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"

	"github.com/containerd/containerd/v2/internal/localaddr"
)

// sddlSocketAdministratorsLocalSystem grants Builtin Administrators and Local
// System full access and nobody else. Unlike SddlAdministratorsLocalSystem it
// has no OI/CI flags, which only apply to containers.
const sddlSocketAdministratorsLocalSystem = "D:P(A;;GA;;;BA)(A;;GA;;;SY)"

// maxUnixPathLen is the longest AF_UNIX path: sun_path is 108 bytes on Windows
// as on Linux (https://lwn.net/Articles/987098/), including the NUL.
const maxUnixPathLen = 107

// GetLocalListener returns a listener for a named pipe or an AF_UNIX socket
// (Windows 10 1803+, Server 2019+), see localaddr.Parse for the address forms.
//
// uid and gid are unused: an AF_UNIX socket is instead restricted by DACL to
// Builtin Administrators and Local System, and named pipes follow go-winio
// defaults.
func GetLocalListener(path string, uid, gid int) (net.Listener, error) {
	network, path, _ := localaddr.Parse(path)
	path = filepath.FromSlash(path)
	if network == localaddr.NamedPipe {
		return winio.ListenPipe(path, nil)
	}
	return createUnixSocket(path)
}

// createUnixSocket listens on an AF_UNIX socket at path. os.Chmod and os.Chown
// do nothing on Windows, so the parent directory is created with
// MkdirAllWithACL and the socket gets a protected DACL.
func createUnixSocket(path string) (net.Listener, error) {
	if len(path) > maxUnixPathLen {
		return nil, fmt.Errorf("%q: unix socket path too long (> %d)", path, maxUnixPathLen)
	}
	if err := MkdirAllWithACL(filepath.Dir(path), 0750); err != nil {
		return nil, err
	}
	if err := removeStaleSocket(path); err != nil {
		return nil, err
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("failed to listen on unix socket %s: %w", path, err)
	}
	// The socket inherits its parent's ACEs until the DACL below replaces
	// them. MkdirAllWithACL closes that window for directories created here;
	// restricting a pre-existing parent is up to the deployer.
	if err := setFileSecurityDescriptor(path, sddlSocketAdministratorsLocalSystem); err != nil {
		l.Close()
		return nil, fmt.Errorf("failed to set security descriptor on unix socket %s: %w", path, err)
	}
	return l, nil
}

// removeStaleSocket removes a file left at path by a previous listener. It
// refuses a directory, which os.Remove would delete if empty on Windows.
func removeStaleSocket(path string) error {
	fi, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if fi.IsDir() {
		return fmt.Errorf("%s is a directory, not a unix socket", path)
	}
	return os.Remove(path)
}

// setFileSecurityDescriptor applies the DACL of sddl to the file at path,
// protected so that it does not inherit ACEs from the parent directory.
func setFileSecurityDescriptor(path, sddl string) error {
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil,
	)
}
