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
	"net"
	"path/filepath"
	"testing"
)

func TestIsNamedPipePath(t *testing.T) {
	testcases := []struct {
		name string
		path string
		want bool
	}{
		{name: "forward slash pipe", path: "//./pipe/containerd", want: true},
		{name: "backslash pipe", path: `\\.\pipe\containerd`, want: true},
		{name: "unix socket path", path: "/tmp/test.sock", want: false},
		{name: "windows fs path", path: `C:\Users\test\docker.sock`, want: false},
	}
	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isNamedPipePath(tc.path); got != tc.want {
				t.Errorf("isNamedPipePath(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

func TestGetLocalListenerUnixSocket(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "test.sock")

	l, err := GetLocalListener(sockPath, 0, 0)
	if err != nil {
		t.Fatalf("GetLocalListener(%q) failed: %v", sockPath, err)
	}
	defer l.Close()

	// Verify we can connect to it
	done := make(chan error, 1)
	go func() {
		conn, err := l.Accept()
		if err == nil {
			conn.Close()
		}
		done <- err
	}()

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	conn.Close()

	if err := <-done; err != nil {
		t.Fatalf("Accept failed: %v", err)
	}
}
