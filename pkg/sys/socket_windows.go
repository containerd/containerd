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
	"strings"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

// sddlSocketAdministratorsLocalSystem grants full access to Builtin
// Administrators and Local System, and to nobody else. Unlike
// SddlAdministratorsLocalSystem it carries no OI/CI inheritance flags: those
// only apply to containers, and a socket has no children.
const sddlSocketAdministratorsLocalSystem = "D:P(A;;GA;;;BA)(A;;GA;;;SY)"

func isNamedPipePath(path string) bool {
	return strings.HasPrefix(filepath.ToSlash(path), "//./pipe/")
}

// GetLocalListener returns a listener for the given path. The path may be:
//   - A Windows named pipe path (\\.\pipe\... or //./pipe/...): named pipe listener
//   - Prefixed with "npipe://": named pipe listener
//   - Prefixed with "unix://": AF_UNIX socket listener
//   - Any other bare path: AF_UNIX socket listener
//
// AF_UNIX sockets are supported on Windows 10 1803+ and Windows Server 2019+.
//
// The scheme prefix, when present, is authoritative for the transport type.
// A "unix://" prefix always results in an AF_UNIX socket, even if the path
// looks like a named pipe path.
//
// Note: the uid and gid parameters are not used on Windows. For AF_UNIX
// sockets, access control is enforced via Windows ACLs: newly-created parent
// directories and the socket file itself are restricted to Builtin
// Administrators and Local System. Named pipe security follows go-winio
// defaults.
func GetLocalListener(path string, uid, gid int) (net.Listener, error) {
	// unix:// is authoritative: always AF_UNIX regardless of path content.
	if rest, ok := strings.CutPrefix(path, "unix://"); ok {
		return createUnixSocket(rest)
	}
	// npipe:// is authoritative: always a named pipe.
	if rest, ok := strings.CutPrefix(filepath.ToSlash(path), "npipe://"); ok {
		return winio.ListenPipe(rest, nil)
	}
	// Bare //./pipe/ or \\.\pipe\ paths are named pipes (backward compatibility).
	if isNamedPipePath(path) {
		return winio.ListenPipe(path, nil)
	}
	// All other bare paths are AF_UNIX sockets.
	return createUnixSocket(path)
}

// createUnixSocket creates an AF_UNIX socket at path and returns a listener.
//
// The parent directory is created (with MkdirAllWithACL) and the socket file
// itself is protected with a DACL granting access to Builtin Administrators
// and Local System only. os.Chmod and os.Chown have no effect on Windows, so
// unlike the Unix implementation this is the only access control available,
// and the uid/gid arguments of GetLocalListener have no analogue.
//
// Windows embraced the 108 byte sun_path limit, same as Linux, see
// https://lwn.net/Articles/987098/
func createUnixSocket(path string) (net.Listener, error) {
	if len(path) > 108 {
		return nil, fmt.Errorf("%q: unix socket path too long (> 108)", path)
	}
	// Use MkdirAllWithACL so newly-created parent directories are restricted
	// to Builtin Administrators and Local System (SddlAdministratorsLocalSystem).
	if err := MkdirAllWithACL(filepath.Dir(path), 0750); err != nil {
		return nil, err
	}
	// Remove an existing socket file so we can rebind cleanly.
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("failed to listen on unix socket %s: %w", path, err)
	}
	// The socket file is created by net.Listen with the ACEs it inherits from
	// its parent, so it is briefly reachable by whoever the parent grants
	// access to before the DACL below replaces them. MkdirAllWithACL closes
	// that window for directories containerd creates itself; a pre-existing
	// parent is the deployer's to restrict.
	if err := setFileSecurityDescriptor(path, sddlSocketAdministratorsLocalSystem); err != nil {
		l.Close()
		return nil, fmt.Errorf("failed to set security descriptor on unix socket %s: %w", path, err)
	}
	return l, nil
}

// setFileSecurityDescriptor applies the DACL from the given SDDL string to
// the named file object. PROTECTED_DACL_SECURITY_INFORMATION prevents the
// inherited parent-directory ACEs from overriding the explicit DACL.
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
