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
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	// required for `-test.root` flag not to fail
	_ "github.com/containerd/continuity/testutil"
)

// shortTempDir returns a temporary directory with a short path: t.TempDir
// embeds the test name, which can overflow the sun_path limit (104 on macOS).
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "sock")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// checkAccept verifies that a connection made by dial reaches l.
func checkAccept(t *testing.T, l net.Listener, dial func() (net.Conn, error)) {
	t.Helper()

	type accepted struct {
		conn net.Conn
		err  error
	}
	done := make(chan accepted, 1)
	go func() {
		conn, err := l.Accept()
		done <- accepted{conn, err}
	}()

	conn, err := dial()
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	// Stay connected until Accept returns: go-winio drops a pipe connection
	// that the client closed first, and Accept then waits for another one.
	defer conn.Close()

	select {
	case a := <-done:
		if a.err != nil {
			t.Fatalf("Accept failed: %v", a.err)
		}
		a.conn.Close()
	case <-time.After(10 * time.Second):
		t.Fatal("Accept did not return")
	}
}

func TestGetLocalListenerUnixSocket(t *testing.T) {
	prefixes := []string{""}
	if runtime.GOOS == "windows" {
		// Only the Windows listener takes the scheme.
		prefixes = append(prefixes, "unix://")
	}
	for _, prefix := range prefixes {
		t.Run("prefix="+prefix, func(t *testing.T) {
			sockPath := filepath.Join(shortTempDir(t), "test.sock")

			l, err := GetLocalListener(prefix+sockPath, os.Getuid(), os.Getgid())
			if err != nil {
				t.Fatalf("GetLocalListener(%q) failed: %v", prefix+sockPath, err)
			}
			defer l.Close()

			checkAccept(t, l, func() (net.Conn, error) { return net.Dial("unix", sockPath) })
		})
	}
}

// TestGetLocalListenerStaleSocket verifies that a socket file left by a
// previous listener does not prevent listening on the same path.
func TestGetLocalListenerStaleSocket(t *testing.T) {
	sockPath := filepath.Join(shortTempDir(t), "test.sock")

	l, err := GetLocalListener(sockPath, os.Getuid(), os.Getgid())
	if err != nil {
		t.Fatalf("GetLocalListener(%q) failed: %v", sockPath, err)
	}
	l.(*net.UnixListener).SetUnlinkOnClose(false)
	l.Close()
	if _, err := os.Lstat(sockPath); err != nil {
		t.Fatalf("expected a stale socket at %q: %v", sockPath, err)
	}

	l, err = GetLocalListener(sockPath, os.Getuid(), os.Getgid())
	if err != nil {
		t.Fatalf("GetLocalListener(%q) over a stale socket failed: %v", sockPath, err)
	}
	defer l.Close()

	checkAccept(t, l, func() (net.Conn, error) { return net.Dial("unix", sockPath) })
}
