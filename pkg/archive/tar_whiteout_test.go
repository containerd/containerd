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

package archive

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestOpaqueWhiteoutKeepsNestedFile covers an opaque marker that follows a
// nested file whose parent directory is only created while extracting that
// file, not by its own tar header.
func TestOpaqueWhiteoutKeepsNestedFile(t *testing.T) {
	apply := func(t *testing.T, withDirHeader bool) {
		t.Helper()
		td := t.TempDir()
		if err := os.MkdirAll(filepath.Join(td, "dir"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(td, "dir", "sub"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(td, "dir", "old"), []byte("old"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(td, "dir", "sub", "old"), []byte("old"), 0o644); err != nil {
			t.Fatal(err)
		}

		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		if withDirHeader {
			if err := tw.WriteHeader(&tar.Header{
				Name:     "dir/sub/",
				Mode:     0o755,
				Typeflag: tar.TypeDir,
			}); err != nil {
				t.Fatal(err)
			}
		}
		body := []byte("new")
		if err := tw.WriteHeader(&tar.Header{
			Name:     "dir/sub/newfile",
			Mode:     0o644,
			Size:     int64(len(body)),
			Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
		if err := tw.WriteHeader(&tar.Header{
			Name:     "dir/" + whiteoutOpaqueDir,
			Mode:     0o600,
			Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if err := tw.Close(); err != nil {
			t.Fatal(err)
		}

		if _, err := Apply(t.Context(), td, bytes.NewReader(buf.Bytes()), WithNoSameOwner()); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(td, "dir", "sub", "newfile"))
		if err != nil {
			t.Fatalf("nested file: %v", err)
		}
		if string(got) != "new" {
			t.Fatalf("nested file content %q", got)
		}
		if _, err := os.Lstat(filepath.Join(td, "dir", "old")); !os.IsNotExist(err) {
			t.Fatalf("lower file still present: %v", err)
		}
		if _, err := os.Lstat(filepath.Join(td, "dir", "sub", "old")); !os.IsNotExist(err) {
			t.Fatalf("lower nested file still present: %v", err)
		}
	}

	t.Run("implicit parent", func(t *testing.T) { apply(t, false) })
	t.Run("explicit dir", func(t *testing.T) { apply(t, true) })
}
