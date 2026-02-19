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

func dialer(address string, timeout time.Duration) (net.Conn, error) {
	// accept npipe://<pipe>
	if strings.HasPrefix(address, "npipe://") {
		address = strings.TrimPrefix(filepath.ToSlash(address), "npipe://")
		return winio.DialPipe(address, &timeout)
	}

	address = strings.TrimPrefix(address, "unix://")
	// accept //./pipe/ and \\.\pipe\
	if isNamedPipePath(address) {
		return winio.DialPipe(address, &timeout)
	}
	// other paths are AF_UNIX, like Unix
	return net.DialTimeout("unix", address, timeout)
}

// DialAddress returns the address with the appropriate scheme prepended.
// Named pipe paths get npipe://, everything else gets unix://.
func DialAddress(address string) string {
	address = filepath.ToSlash(address)
	if isNamedPipePath(address) {
		if !strings.HasPrefix(address, "npipe://") {
			address = fmt.Sprintf("npipe://%s", address)
		}
		return address
	}
	if !strings.HasPrefix(address, "unix://") {
		address = fmt.Sprintf("unix://%s", address)
	}
	return address
}
