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

package stackdump

import (
	"os"
	"syscall"
	"testing"
)

// TestWriteFilePermissions pins the dump file to owner-only. A goroutine dump
// exposes the internals of a process that usually runs as root, and the file
// lands in a world-readable temp directory.
func TestWriteFilePermissions(t *testing.T) {
	setTempDir(t, t.TempDir())

	// os.WriteFile applies the umask, so clear it for the duration.
	old := syscall.Umask(0)
	defer syscall.Umask(old)

	name, err := WriteFile("test-prefix", []byte("dump"))
	if err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	fi, err := os.Stat(name)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Fatalf("stack dump file mode = %04o; want 0600", got)
	}
}
