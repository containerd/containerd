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

package internal

import (
	"net"
	"path/filepath"

	"github.com/containerd/containerd/v2/internal/localaddr"
)

// IsLocalAddress reports whether path is a named pipe or an AF_UNIX socket
// rather than a TCP address, see localaddr.Parse. A bare path is a socket if it
// is absolute, has a drive letter or has no port, as GetLocalListener takes it.
func IsLocalAddress(path string) bool {
	if _, _, explicit := localaddr.Parse(path); explicit || filepath.IsAbs(path) || hasDriveLetter(path) {
		return true
	}
	_, _, err := net.SplitHostPort(path)
	return err != nil
}

// hasDriveLetter reports whether path starts with a drive letter, as in
// "C:debug.sock", which SplitHostPort would take for a host and a port.
func hasDriveLetter(path string) bool {
	if len(path) < 2 || path[1] != ':' {
		return false
	}
	c := path[0]
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z'
}
