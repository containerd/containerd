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
