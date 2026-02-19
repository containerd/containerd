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
	"path/filepath"
	"testing"
	"time"
)

func TestDialAddressScheme(t *testing.T) {
	testcases := []struct {
		name    string
		address string
		want    string
	}{
		{name: "pipe gets npipe", address: "//./pipe/docker", want: "npipe:////./pipe/docker"},
		{name: "unix path gets unix", address: "/tmp/docker.sock", want: "unix:///tmp/docker.sock"},
		{name: "windows path gets unix", address: "C:/Users/test/docker.sock", want: "unix://C:/Users/test/docker.sock"},
	}
	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DialAddress(tc.address); got != tc.want {
				t.Errorf("DialAddress(%q) = %q, want %q", tc.address, got, tc.want)
			}
		})
	}
}

func TestDialerUnixSocket(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "test.sock")

	l, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("Listen failed: %v", err)
	}
	defer l.Close()

	go func() {
		conn, err := l.Accept()
		if err == nil {
			conn.Close()
		}
	}()

	conn, err := dialer(sockPath, 5*time.Second)
	if err != nil {
		t.Fatalf("dialer(%q) failed: %v", sockPath, err)
	}
	conn.Close()
}
