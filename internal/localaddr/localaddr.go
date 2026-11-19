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

// Package localaddr parses the local (non-TCP) addresses containerd listens
// on and dials on Windows: AF_UNIX sockets and named pipes. It is plain string
// handling, so that it can be shared and tested on every platform.
package localaddr

import "strings"

// Network is the kind of local endpoint an address refers to.
type Network int

const (
	// Unix is an AF_UNIX socket.
	Unix Network = iota
	// NamedPipe is a Windows named pipe.
	NamedPipe
)

// Parse classifies a local address and returns the path to give to net.Listen
// (Unix) or go-winio (NamedPipe). An address is one of:
//   - "unix://<path>" or "unix:<path>": an AF_UNIX socket. gRPC passes a
//     custom dialer the first form for a path that is absolute on the host
//     and the second otherwise.
//   - "npipe://<pipe>": a named pipe, as "npipe:////./pipe/<name>" or
//     "npipe://./pipe/<name>".
//   - "//./pipe/<name>" or `\\.\pipe\<name>`: a named pipe.
//   - anything else: an AF_UNIX socket.
//
// A scheme is authoritative. The path of a named pipe is always
// "//./pipe/<name>"; that of a Unix socket is returned as written, for the
// caller to convert to the host's separators. explicit is false for a bare
// path that merely defaults to Unix.
func Parse(address string) (network Network, path string, explicit bool) {
	if rest, ok := strings.CutPrefix(address, "unix:"); ok {
		rest = strings.TrimPrefix(rest, "//")
		// Also accept "unix:///C:/<path>", as in a file URL, and gRPC's
		// "unix:/C:/<path>".
		if len(rest) >= 3 && rest[0] == '/' && rest[2] == ':' {
			rest = rest[1:]
		}
		return Unix, rest, true
	}
	slashed := strings.ReplaceAll(address, `\`, "/")
	if rest, ok := strings.CutPrefix(slashed, "npipe://"); ok {
		// The authority of "npipe://./pipe/<name>" is ".".
		if hasPrefixFold(rest, "./pipe/") {
			rest = "//" + rest
		}
		return NamedPipe, rest, true
	}
	if hasPrefixFold(slashed, "//./pipe/") {
		return NamedPipe, slashed, true
	}
	return Unix, address, false
}

// hasPrefixFold is strings.HasPrefix ignoring case, as the pipe namespace is.
func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}
