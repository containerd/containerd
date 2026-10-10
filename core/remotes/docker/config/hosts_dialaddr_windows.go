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

package config

import (
	"fmt"
	"net/http"
	"time"
)

// dial_addr is deliberately not supported on Windows.
//
// The security value of dial_addr comes from Unix domain sockets being a Unix
// primitive: a pathname socket is protected by filesystem permissions and, in a
// container context, by mount-namespace isolation. AF_UNIX and the surrounding
// networking stack behave differently on Windows, so the same guarantees do not
// carry over unchanged. Support was therefore deferred pending a specific
// Windows use case and its own design review — see
// https://github.com/containerd/containerd/issues/14224 and PR #13569.
//
// Please do not re-enable Windows here without that design discussion: the POSIX
// implementation lives in hosts_dialaddr_unix.go.
func parseDialAddr(raw string) (string, error) {
	return "", fmt.Errorf("dial_addr is not supported on Windows: %q", raw)
}

// applyDialAddr is unreachable on Windows because parseDialAddr rejects dial_addr
// before it is ever stored, so this is a no-op that only exists to satisfy the
// shared caller in hosts.go.
func applyDialAddr(tr *http.Transport, addr string, timeout time.Duration) {}
