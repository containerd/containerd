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

package sys

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

// TestGetLocalListenerScheme verifies that a "unix://" prefix is stripped
// before the path is used, on Unix as well as on Windows, so that a schemed
// address in the daemon configuration listens where it says it does rather
// than under a directory literally named "unix:".
func TestGetLocalListenerScheme(t *testing.T) {
	for _, prefix := range []string{"", "unix://"} {
		t.Run("prefix="+prefix, func(t *testing.T) {
			sockPath := filepath.Join(t.TempDir(), "test.sock")

			l, err := GetLocalListener(prefix+sockPath, os.Getuid(), os.Getgid())
			if err != nil {
				t.Fatalf("GetLocalListener(%q) failed: %v", prefix+sockPath, err)
			}
			defer l.Close()

			if _, err := os.Stat(sockPath); err != nil {
				t.Fatalf("no socket at %q: %v", sockPath, err)
			}

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
		})
	}
}
