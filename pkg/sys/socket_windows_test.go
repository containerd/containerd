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
	"strings"
	"testing"

	"golang.org/x/sys/windows"
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

// TestGetLocalListenerUnixSocketWithScheme verifies that a "unix://" prefix is
// stripped before use, so that callers may pass a schemed address directly.
func TestGetLocalListenerUnixSocketWithScheme(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "test.sock")
	schemed := "unix://" + sockPath

	l, err := GetLocalListener(schemed, 0, 0)
	if err != nil {
		t.Fatalf("GetLocalListener(%q) failed: %v", schemed, err)
	}
	defer l.Close()

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
		t.Fatalf("Dial(%q) failed: %v", sockPath, err)
	}
	conn.Close()

	if err := <-done; err != nil {
		t.Fatalf("Accept failed: %v", err)
	}
}

// TestCreateUnixSocketPathTooLong verifies that paths exceeding the 108-byte
// sun_path limit are rejected with an error rather than passed to the OS.
func TestCreateUnixSocketPathTooLong(t *testing.T) {
	// Construct a path that is definitely more than 108 bytes long.
	longPath := strings.Repeat("a", 109)
	_, err := createUnixSocket(longPath)
	if err == nil {
		t.Fatal("expected error for path > 108 bytes, got nil")
	}
}

// TestCreateUnixSocketDACL verifies that the socket is reachable only by
// Builtin Administrators and Local System. The API socket is root-equivalent,
// so a socket left with its inherited (potentially world-readable) ACL would
// be a privilege escalation. This also confirms that SetNamedSecurityInfo
// accepts an AF_UNIX socket path at all, which the implementation relies on.
func TestCreateUnixSocketDACL(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "test.sock")

	l, err := createUnixSocket(sockPath)
	if err != nil {
		t.Fatalf("createUnixSocket(%q) failed: %v", sockPath, err)
	}
	defer l.Close()

	sd, err := windows.GetNamedSecurityInfo(sockPath, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("GetNamedSecurityInfo(%q) failed: %v", sockPath, err)
	}

	// The DACL must be protected, otherwise inherited ACEs from the parent
	// directory are merged in and can widen access.
	control, _, err := sd.Control()
	if err != nil {
		t.Fatalf("Control failed: %v", err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Errorf("DACL is not protected: control = %#x, sd = %q", control, sd)
	}

	// Exactly two ACEs, for the two principals named in the SDDL. Any third
	// grants access to somebody we did not intend.
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatalf("DACL failed: %v", err)
	}
	if dacl == nil {
		t.Fatal("socket has a NULL DACL, which grants everyone full access")
	}
	if dacl.AceCount != 2 {
		t.Errorf("DACL has %d ACEs, want 2: %q", dacl.AceCount, sd)
	}
	for _, sid := range []string{";;;BA)", ";;;SY)"} {
		if !strings.Contains(sd.String(), sid) {
			t.Errorf("DACL %q is missing an ACE for %q", sd, sid)
		}
	}
}
