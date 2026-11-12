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

import "testing"

func TestIsLocalAddress(t *testing.T) {
	testcases := []struct {
		address string
		want    bool
	}{
		// Named pipes.
		{address: `\\.\pipe\containerd-debug`, want: true},
		{address: "//./pipe/containerd-debug", want: true},
		{address: `\\.\PIPE\containerd-debug`, want: true},
		{address: "npipe:////./pipe/containerd-debug", want: true},
		{address: "npipe://./pipe/containerd-debug", want: true},

		// AF_UNIX sockets.
		{address: "unix:///run/containerd/debug.sock", want: true},
		{address: `unix://C:\run\containerd\debug.sock`, want: true},
		{address: `C:\run\containerd\debug.sock`, want: true},
		{address: "debug.sock", want: true},
		{address: "C:debug.sock", want: true},
		{address: `run\debug.sock`, want: true},

		// TCP.
		{address: "127.0.0.1:6060", want: false},
		{address: "localhost:6060", want: false},
		{address: ":6060", want: false},
		{address: "[::1]:6060", want: false},
	}
	for _, tc := range testcases {
		t.Run(tc.address, func(t *testing.T) {
			if got := IsLocalAddress(tc.address); got != tc.want {
				t.Errorf("IsLocalAddress(%q) = %v, want %v", tc.address, got, tc.want)
			}
		})
	}
}
