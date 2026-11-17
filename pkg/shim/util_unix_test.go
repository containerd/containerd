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

package shim

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMkdirSocketDirKeepsDefaultStateTraversable(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "containerd")
	socketDir := filepath.Join(root, "s")
	if err := mkdirSocketDir(socketDir, root); err != nil {
		t.Fatal(err)
	}
	assertPerm(t, root, 0o711)
	assertPerm(t, socketDir, 0o700)

	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := mkdirSocketDir(socketDir, root); err != nil {
		t.Fatal(err)
	}
	assertPerm(t, root, 0o711)

	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := mkdirSocketDir(socketDir, root); err != nil {
		t.Fatal(err)
	}
	assertPerm(t, root, 0o755)

	other := filepath.Join(base, "custom", "s")
	if err := mkdirSocketDir(other, root); err != nil {
		t.Fatal(err)
	}
	assertPerm(t, filepath.Dir(other), 0o700)
	assertPerm(t, other, 0o700)
}

func assertPerm(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %o, want %o", path, got, want)
	}
}

func TestNewSocket(t *testing.T) {
	t.Run("socket in nested directory", func(t *testing.T) {
		dir, err := os.MkdirTemp("/tmp", "shim-test-")
		if err != nil {
			t.Fatalf("failed to create temp dir: %v", err)
		}
		t.Cleanup(func() { os.RemoveAll(dir) })

		address := fmt.Sprintf("unix://%s/a/b/test.sock", dir)

		l, err := NewSocket(address)
		if err != nil {
			t.Fatalf("NewSocket failed: %v", err)
		}
		t.Cleanup(func() { l.Close() })

		conn, err := net.DialTimeout("unix", socket(address).path(), time.Second)
		if err != nil {
			t.Fatalf("failed to connect to socket: %v", err)
		}
		conn.Close()
	})

	t.Run("socket in existing directory", func(t *testing.T) {
		dir, err := os.MkdirTemp("/tmp", "shim-test-")
		if err != nil {
			t.Fatalf("failed to create temp dir: %v", err)
		}
		t.Cleanup(func() { os.RemoveAll(dir) })

		address := "unix://" + filepath.Join(dir, "test.sock")

		l, err := NewSocket(address)
		if err != nil {
			t.Fatalf("NewSocket failed: %v", err)
		}
		t.Cleanup(func() { l.Close() })

		conn, err := net.DialTimeout("unix", socket(address).path(), time.Second)
		if err != nil {
			t.Fatalf("failed to connect to socket: %v", err)
		}
		conn.Close()
	})
}
