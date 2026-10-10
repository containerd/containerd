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

package config

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// maxUnixSocketPathLen is the size of sun_path in struct sockaddr_un, taken
// from the platform's own sockaddr definition: 108 on Linux, 104 on the
// BSDs/macOS. The limit matches the OS we build for.
const maxUnixSocketPathLen = len(syscall.RawSockaddrUnix{}.Path)

// parseDialAddr validates a hosts.toml "dial_addr" and returns the unix socket
// path for net.Dial("unix", ...).
//
// Only a pathname socket is accepted: "unix:///absolute/path.sock". Abstract
// sockets ("unix://@name") are intentionally rejected — they have no filesystem
// permissions and live in the network namespace rather than the mount
// namespace, which is the reachability weakness behind CVE-2020-15257. The
// security model of dial_addr relies on filesystem permissions plus
// mount-namespace isolation, so only filesystem-backed sockets are allowed.
//
// dial_addr is set by the operator, so the checks below aim to catch typos
// early with a clear message.
func parseDialAddr(raw string) (string, error) {
	// Extra spaces are usually a copy-paste slip.
	if strings.TrimSpace(raw) != raw {
		return "", fmt.Errorf("dial_addr %q must not have leading or trailing spaces", raw)
	}
	// Must begin with "unix://". The scheme is not case-sensitive.
	const prefix = "unix://"
	if len(raw) < len(prefix) || !strings.EqualFold(raw[:len(prefix)], prefix) {
		return "", fmt.Errorf("dial_addr %q must start with \"unix://\"", raw)
	}
	if _, err := url.Parse(raw); err != nil {
		return "", fmt.Errorf("unable to parse dial_addr %q: %w", raw, err)
	}
	// The text after "unix://" is used verbatim as the socket address, so a
	// "?" or "#" anywhere in it would end up in the path handed to net.Dial.
	// Reject them outright — including a bare trailing delimiter with an
	// empty query or fragment, which url.Parse does not surface.
	if strings.Contains(raw, "?") {
		return "", fmt.Errorf("dial_addr %q must not contain a \"?\" query", raw)
	}
	if strings.Contains(raw, "#") {
		return "", fmt.Errorf("dial_addr %q must not contain a \"#\" fragment", raw)
	}
	addr := raw[len(prefix):]
	if addr == "" {
		return "", fmt.Errorf("dial_addr %q has no socket path after \"unix://\"", raw)
	}
	// Abstract sockets are not supported; see the function doc.
	if strings.HasPrefix(addr, "@") {
		return "", fmt.Errorf(
			"dial_addr %q uses an abstract socket (\"unix://@name\"), which is not supported; use a pathname socket like \"unix:///run/foo.sock\"",
			raw,
		)
	}
	// The address must fit the OS limit for sun_path.
	if len(addr) > maxUnixSocketPathLen {
		return "", fmt.Errorf(
			"dial_addr socket path is too long: %d bytes, max is %d: %q",
			len(addr), maxUnixSocketPathLen, raw,
		)
	}
	// A pathname socket must be absolute.
	if !strings.HasPrefix(addr, "/") {
		return "", fmt.Errorf(
			"dial_addr %q must be an absolute path like \"unix:///run/foo.sock\" (note the three slashes)",
			raw,
		)
	}
	if strings.HasSuffix(addr, "/") {
		return "", fmt.Errorf(
			"dial_addr %q ends with \"/\"; it must point to a socket file, not a directory",
			raw,
		)
	}
	return addr, nil
}

// dialContextFunc is the http.Transport.DialContext signature. Named so the
// unixDialContext signature stays readable.
type dialContextFunc func(ctx context.Context, network, address string) (net.Conn, error)

// unixDialContext returns a DialContext that ignores the requested network and
// address and instead dials the configured unix socket.
func unixDialContext(addr string, timeout time.Duration) dialContextFunc {
	d := &net.Dialer{
		Timeout:   timeout,
		KeepAlive: 30 * time.Second,
	}
	return func(ctx context.Context, _, _ string) (net.Conn, error) {
		return d.DialContext(ctx, "unix", addr)
	}
}

// applyDialAddr wires a parsed dial_addr onto a per-host transport: it dials the
// unix socket directly and clears Proxy so HTTP(S)_PROXY from the environment
// never reroutes the connection.
func applyDialAddr(tr *http.Transport, addr string, timeout time.Duration) {
	tr.DialContext = unixDialContext(addr, timeout)
	tr.Proxy = nil
}
