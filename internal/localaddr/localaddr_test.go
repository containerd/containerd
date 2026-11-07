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

package localaddr

import "testing"

func TestParse(t *testing.T) {
	testcases := []struct {
		name         string
		address      string
		wantNetwork  Network
		wantPath     string
		wantExplicit bool
	}{
		// Named pipes without a scheme (backward compatibility).
		{name: "pipe with slashes", address: "//./pipe/containerd", wantNetwork: NamedPipe, wantPath: "//./pipe/containerd", wantExplicit: true},
		{name: "pipe with backslashes", address: `\\.\pipe\containerd`, wantNetwork: NamedPipe, wantPath: "//./pipe/containerd", wantExplicit: true},
		{name: "pipe namespace is case insensitive", address: `\\.\PIPE\Containerd`, wantNetwork: NamedPipe, wantPath: "//./PIPE/Containerd", wantExplicit: true},

		// Named pipes with a scheme.
		{name: "npipe canonical", address: "npipe:////./pipe/containerd", wantNetwork: NamedPipe, wantPath: "//./pipe/containerd", wantExplicit: true},
		{name: "npipe short", address: "npipe://./pipe/containerd", wantNetwork: NamedPipe, wantPath: "//./pipe/containerd", wantExplicit: true},
		{name: "npipe short case insensitive", address: "npipe://./PIPE/containerd", wantNetwork: NamedPipe, wantPath: "//./PIPE/containerd", wantExplicit: true},
		{name: "npipe with backslashes", address: `npipe://\\.\pipe\containerd`, wantNetwork: NamedPipe, wantPath: "//./pipe/containerd", wantExplicit: true},

		// AF_UNIX sockets with a scheme.
		{name: "unix absolute", address: "unix:///tmp/containerd.sock", wantNetwork: Unix, wantPath: "/tmp/containerd.sock", wantExplicit: true},
		{name: "unix drive letter", address: `unix://C:\run\containerd.sock`, wantNetwork: Unix, wantPath: `C:\run\containerd.sock`, wantExplicit: true},
		{name: "unix opaque drive letter", address: "unix:C:/run/containerd.sock", wantNetwork: Unix, wantPath: "C:/run/containerd.sock", wantExplicit: true},
		{name: "unix file URL drive letter", address: "unix:///C:/run/containerd.sock", wantNetwork: Unix, wantPath: "C:/run/containerd.sock", wantExplicit: true},
		{name: "unix opaque absolute", address: "unix:/tmp/containerd.sock", wantNetwork: Unix, wantPath: "/tmp/containerd.sock", wantExplicit: true},
		// The scheme is authoritative, whatever the path looks like.
		{name: "unix scheme wins over pipe path", address: "unix:////./pipe/containerd", wantNetwork: Unix, wantPath: "//./pipe/containerd", wantExplicit: true},

		// Bare paths default to AF_UNIX.
		{name: "bare absolute", address: "/tmp/containerd.sock", wantNetwork: Unix, wantPath: "/tmp/containerd.sock"},
		{name: "bare drive letter", address: `C:\run\containerd.sock`, wantNetwork: Unix, wantPath: `C:\run\containerd.sock`},
		{name: "bare relative", address: "containerd.sock", wantNetwork: Unix, wantPath: "containerd.sock"},
		{name: "not a pipe namespace", address: "//./notpipe/containerd", wantNetwork: Unix, wantPath: "//./notpipe/containerd"},
	}
	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			network, path, explicit := Parse(tc.address)
			if network != tc.wantNetwork || path != tc.wantPath || explicit != tc.wantExplicit {
				t.Errorf("Parse(%q) = (%v, %q, %v), want (%v, %q, %v)",
					tc.address, network, path, explicit, tc.wantNetwork, tc.wantPath, tc.wantExplicit)
			}
		})
	}
}
