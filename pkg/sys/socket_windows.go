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
)

func isNamedPipePath(path string) bool {
	return strings.HasPrefix(filepath.ToSlash(path), "//./pipe/")
}

// GetLocalListener returns a listener for the given path. If the path is a
// Windows named pipe (\\.\pipe\...), it creates a named pipe listener.
// Otherwise, it creates an AF_UNIX socket listener, which is supported on
// Windows 10 1803+ and Windows Server 2019+.
func GetLocalListener(path string, uid, gid int) (net.Listener, error) {
	if isNamedPipePath(path) {
		return winio.ListenPipe(path, nil)
	}

	return createUnixSocket(path)
}

func createUnixSocket(path string) (net.Listener, error) {
	// Windows embraced the 108 limit, same as Linux
	// see https://lwn.net/Articles/987098/
	if len(path) > 108 {
		return nil, fmt.Errorf("%q: unix socket path too long (> 108)", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0660); err != nil {
		return nil, err
	}
	// Remove existing socket file if present.
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("failed to listen on unix socket %s: %w", path, err)
	}
	return l, nil
}
