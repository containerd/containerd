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
	"strings"
	"testing"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

// TestGetLocalListenerNamedPipe verifies that every spelling of a pipe
// address listens on the same pipe.
func TestGetLocalListenerNamedPipe(t *testing.T) {
	name := "containerd-test-" + strings.ReplaceAll(t.Name(), "/", "-")
	for _, address := range []string{
		`\\.\pipe\` + name,
		"//./pipe/" + name,
		"npipe:////./pipe/" + name,
		"npipe://./pipe/" + name,
	} {
		t.Run(address, func(t *testing.T) {
			l, err := GetLocalListener(address, 0, 0)
			if err != nil {
				t.Fatalf("GetLocalListener(%q) failed: %v", address, err)
			}
			defer l.Close()

			checkAccept(t, l, func() (net.Conn, error) { return winio.DialPipe("//./pipe/"+name, nil) })
		})
	}
}

// pathOfLen returns a path of exactly n bytes in dir.
func pathOfLen(t *testing.T, dir string, n int) string {
	t.Helper()
	name := n - len(dir) - 1
	if name < 1 {
		t.Skipf("%q leaves no room for a file name in %d bytes", dir, n)
	}
	return filepath.Join(dir, strings.Repeat("a", name))
}

// TestCreateUnixSocketPathLength verifies that a path over the sun_path limit
// is rejected here rather than passed to the OS.
func TestCreateUnixSocketPathLength(t *testing.T) {
	dir := shortTempDir(t)

	l, err := createUnixSocket(pathOfLen(t, dir, maxUnixPathLen))
	if err != nil {
		t.Fatalf("a path of %d bytes should be accepted: %v", maxUnixPathLen, err)
	}
	l.Close()

	for _, n := range []int{maxUnixPathLen + 1, 2 * maxUnixPathLen} {
		path := pathOfLen(t, dir, n)
		l, err := createUnixSocket(path)
		if err == nil {
			l.Close()
			t.Fatalf("a path of %d bytes should be rejected", n)
		}
		if !strings.Contains(err.Error(), "too long") {
			t.Errorf("path of %d bytes: got %v, want a \"too long\" error", n, err)
		}
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Errorf("a rejected path must not be created: %v", err)
		}
	}
}

// TestCreateUnixSocketOverDirectory verifies that a directory at the socket
// path is rejected and left alone, as os.Remove would delete an empty one.
func TestCreateUnixSocketOverDirectory(t *testing.T) {
	path := filepath.Join(shortTempDir(t), "test.sock")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}

	l, err := createUnixSocket(path)
	if err == nil {
		l.Close()
		t.Fatal("expected an error for a socket path that is a directory")
	}
	if fi, err := os.Lstat(path); err != nil || !fi.IsDir() {
		t.Errorf("the directory at %q must be left in place: %v", path, err)
	}
}

// TestCreateUnixSocketDACL verifies that only Builtin Administrators and Local
// System can reach the socket, which is root-equivalent.
func TestCreateUnixSocketDACL(t *testing.T) {
	sockPath := filepath.Join(shortTempDir(t), "test.sock")

	l, err := createUnixSocket(sockPath)
	if err != nil {
		t.Fatalf("createUnixSocket(%q) failed: %v", sockPath, err)
	}
	defer l.Close()

	sd, err := windows.GetNamedSecurityInfo(sockPath, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("GetNamedSecurityInfo(%q) failed: %v", sockPath, err)
	}

	// Protected, or ACEs inherited from the parent could widen access.
	control, _, err := sd.Control()
	if err != nil {
		t.Fatalf("Control failed: %v", err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Errorf("DACL is not protected: control = %#x, sd = %q", control, sd)
	}

	// Exactly the two ACEs of the SDDL.
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
