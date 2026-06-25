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

package dialer

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	winio "github.com/Microsoft/go-winio"
)

func isNoent(err error) bool {
	return os.IsNotExist(err)
}

func isNamedPipePath(path string) bool {
	return strings.HasPrefix(filepath.ToSlash(path), "//./pipe/")
}

// dialer connects to address using the transport determined by the scheme
// prefix, or by path content for bare (unschemed) addresses:
//
//   - "unix://" → AF_UNIX socket (always, regardless of path content)
//   - "npipe://" → Windows named pipe
//   - bare "//./pipe/..." or "\\.\pipe\..." path → named pipe (backward compat)
//   - any other bare path → AF_UNIX socket
//
// The scheme prefix, when present, is authoritative. A "unix://" address is
// always dialed as an AF_UNIX socket even if the path looks like a named pipe.
func dialer(address string, timeout time.Duration) (net.Conn, error) {
	// unix:// is authoritative: always AF_UNIX regardless of path content.
	if rest, ok := strings.CutPrefix(address, "unix://"); ok {
		return net.DialTimeout("unix", rest, timeout)
	}
	// npipe:// is authoritative: always a named pipe.
	if rest, ok := strings.CutPrefix(filepath.ToSlash(address), "npipe://"); ok {
		return winio.DialPipe(rest, &timeout)
	}
	// Bare //./pipe/ or \\.\pipe\ paths: named pipe (backward compatibility).
	if isNamedPipePath(address) {
		return winio.DialPipe(address, &timeout)
	}
	// Bare filesystem paths: AF_UNIX socket.
	return net.DialTimeout("unix", address, timeout)
}

// DialAddress returns the address with the appropriate scheme prepended.
//
// If the address already carries a "npipe://" or "unix://" scheme it is
// returned unchanged (idempotent). For bare paths, named pipe paths
// (//./pipe/... or \\.\pipe\...) receive the "npipe://" scheme; all other
// paths receive "unix://".
func DialAddress(address string) string {
	// Already scheme-prefixed: return as-is (idempotent).
	if strings.HasPrefix(address, "npipe://") || strings.HasPrefix(address, "unix://") {
		return address
	}
	address = filepath.ToSlash(address)
	if isNamedPipePath(address) {
		return fmt.Sprintf("npipe://%s", address)
	}
	return fmt.Sprintf("unix://%s", address)
}
