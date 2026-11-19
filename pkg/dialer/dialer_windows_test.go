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
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Microsoft/go-winio"
)

func TestDialAddressScheme(t *testing.T) {
	testcases := []struct {
		name    string
		address string
		want    string
	}{
		// Bare paths.
		{name: "pipe gets npipe", address: "//./pipe/docker", want: "npipe:////./pipe/docker"},
		{name: "backslash pipe gets npipe", address: `\\.\pipe\docker`, want: "npipe:////./pipe/docker"},
		{name: "unix path gets unix", address: "/tmp/docker.sock", want: "unix:///tmp/docker.sock"},
		// gRPC rejects "unix://C:/...", so a drive letter path gets a slash.
		{name: "windows path gets unix", address: "C:/Users/test/docker.sock", want: "unix:///C:/Users/test/docker.sock"},
		{name: "windows backslash path gets unix", address: `C:\Users\test\docker.sock`, want: "unix:///C:/Users/test/docker.sock"},

		// Schemed addresses are normalized.
		{name: "npipe canonical", address: "npipe:////./pipe/docker", want: "npipe:////./pipe/docker"},
		{name: "npipe short", address: "npipe://./pipe/docker", want: "npipe:////./pipe/docker"},
		{name: "npipe with backslashes", address: `npipe://\\.\pipe\docker`, want: "npipe:////./pipe/docker"},
		{name: "unix absolute", address: "unix:///tmp/docker.sock", want: "unix:///tmp/docker.sock"},
		{name: "unix opaque", address: "unix:C:/Users/test/docker.sock", want: "unix:///C:/Users/test/docker.sock"},
		{name: "unix with authority", address: `unix://C:\Users\test\docker.sock`, want: "unix:///C:/Users/test/docker.sock"},
	}
	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			got := DialAddress(tc.address)
			if got != tc.want {
				t.Errorf("DialAddress(%q) = %q, want %q", tc.address, got, tc.want)
			}
			if again := DialAddress(got); again != got {
				t.Errorf("DialAddress is not idempotent: DialAddress(%q) = %q", got, again)
			}
		})
	}
}

// TestDialAddressEscapes verifies that '#', '%' and spaces in a socket path,
// absolute or relative, reach ContextDialer unchanged through gRPC.
func TestDialAddressEscapes(t *testing.T) {
	for _, name := range []string{"a#b.sock", "a%23b.sock", "a b.sock"} {
		t.Run(name, func(t *testing.T) {
			l, sockPath := listenUnix(t, name)
			connectGRPC(t, l, sockPath)
		})
		t.Run("relative "+name, func(t *testing.T) {
			l, sockPath := listenUnix(t, name)
			t.Chdir(filepath.Dir(sockPath))
			connectGRPC(t, l, name)
		})
	}
}

// TestDialerNamedPipe verifies that every spelling of a pipe address reaches
// the same pipe.
func TestDialerNamedPipe(t *testing.T) {
	name := "containerd-test-" + strings.ReplaceAll(t.Name(), "/", "-")
	l, err := winio.ListenPipe("//./pipe/"+name, nil)
	if err != nil {
		t.Fatalf("ListenPipe failed: %v", err)
	}
	defer l.Close()
	go acceptAndClose(l)

	for _, address := range []string{
		`\\.\pipe\` + name,
		"//./pipe/" + name,
		"npipe:////./pipe/" + name,
		"npipe://./pipe/" + name,
	} {
		t.Run(address, func(t *testing.T) {
			conn, err := dialer(address, 5*time.Second)
			if err != nil {
				t.Fatalf("dialer(%q) failed: %v", address, err)
			}
			conn.Close()
		})
	}
}
