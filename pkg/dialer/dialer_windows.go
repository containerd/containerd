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
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	winio "github.com/Microsoft/go-winio"

	"github.com/containerd/containerd/v2/internal/localaddr"
)

func isNoent(err error) bool {
	return os.IsNotExist(err)
}

// dialer connects to a named pipe or an AF_UNIX socket, see localaddr.Parse.
func dialer(address string, timeout time.Duration) (net.Conn, error) {
	network, path, _ := localaddr.Parse(address)
	if network == localaddr.NamedPipe {
		return winio.DialPipe(path, &timeout)
	}
	return net.DialTimeout("unix", filepath.FromSlash(path), timeout)
}

// DialAddress returns address as a gRPC target: "npipe://" for a named pipe,
// "unix://" for an AF_UNIX socket. The socket path is URL escaped, as gRPC
// unescapes it before ContextDialer, so apply this only once.
func DialAddress(address string) string {
	network, path, _ := localaddr.Parse(address)
	if network == localaddr.NamedPipe {
		return "npipe://" + path
	}
	path = filepath.ToSlash(path)
	// The opaque "unix:<path>" of a relative path cannot hold '#' or '?'.
	if !strings.HasPrefix(path, "/") && !filepath.IsAbs(path) {
		if abs, err := filepath.Abs(path); err == nil {
			path = filepath.ToSlash(abs)
		}
	}
	// gRPC rejects "unix://C:/..." for its authority.
	if len(path) >= 2 && path[1] == ':' {
		path = "/" + path
	}
	if !strings.HasPrefix(path, "/") {
		return "unix:" + path
	}
	return (&url.URL{Scheme: "unix", Path: path}).String()
}
