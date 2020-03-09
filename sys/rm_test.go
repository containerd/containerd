// +build !linux

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
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/docker/docker/pkg/mount"
)

func TestEnsureRemoveAllNotExist(t *testing.T) {
	// should never return an error for a non-existent path
	if err := EnsureRemoveAll("/non/existent/path"); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureRemoveAllWithDir(t *testing.T) {
	dir, err := ioutil.TempDir("", "test-ensure-removeall-with-dir")
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureRemoveAll(dir); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureRemoveAllWithFile(t *testing.T) {
	tmp, err := ioutil.TempFile("", "test-ensure-removeall-with-dir")
	if err != nil {
		t.Fatal(err)
	}
	tmp.Close()
	if err := EnsureRemoveAll(tmp.Name()); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureRemoveAllWithMount(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mount not supported on Windows")
	}
	if os.Getuid() != 0 {
		t.Skip("skipping test that requires root")
	}

	dir1, err := ioutil.TempDir("", "test-ensure-removeall-with-dir1")
	if err != nil {
		t.Fatal(err)
	}
	dir2, err := ioutil.TempDir("", "test-ensure-removeall-with-dir2")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir2)

	bindDir := filepath.Join(dir1, "bind")
	if err := os.MkdirAll(bindDir, 0755); err != nil {
		t.Fatal(err)
	}

	if err := mount.Mount(dir2, bindDir, "none", "bind"); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		err = EnsureRemoveAll(dir1)
		close(done)
	}()

	select {
	case <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for EnsureRemoveAll to finish")
	}

	if _, err := os.Stat(dir1); !os.IsNotExist(err) {
		t.Fatalf("expected %q to not exist", dir1)
	}
}
