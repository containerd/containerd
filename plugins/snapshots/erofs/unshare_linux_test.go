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

package erofs

import (
	"bytes"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func nlink(t *testing.T, path string) uint64 {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return uint64(fi.Sys().(*syscall.Stat_t).Nlink)
}

func TestUnshareIfLinked(t *testing.T) {
	dir := t.TempDir()
	blob := filepath.Join(dir, "blob")
	layer := filepath.Join(dir, "layer.erofs")
	data := []byte("layer data")
	if err := os.WriteFile(blob, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(blob, layer); err != nil {
		t.Fatal(err)
	}

	if err := unshareIfLinked(layer); err != nil {
		t.Fatal(err)
	}

	if n := nlink(t, blob); n != 1 {
		t.Errorf("content blob still shares its inode (nlink=%d)", n)
	}
	if n := nlink(t, layer); n != 1 {
		t.Errorf("layer still shares its inode (nlink=%d)", n)
	}
	got, err := os.ReadFile(layer)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Error("layer content changed by unsharing")
	}
}

func TestUnshareIfLinkedSingleLink(t *testing.T) {
	dir := t.TempDir()
	layer := filepath.Join(dir, "layer.erofs")
	if err := os.WriteFile(layer, []byte("layer data"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(layer)
	if err != nil {
		t.Fatal(err)
	}

	if err := unshareIfLinked(layer); err != nil {
		t.Fatal(err)
	}

	after, err := os.Stat(layer)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Error("single-link file was needlessly replaced")
	}
}
