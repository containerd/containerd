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

package proxy

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// TestOpenLocalShareDelete verifies that the remote store, a separate
// process, can delete or rename a blob while containerd has it open.
func TestOpenLocalShareDelete(t *testing.T) {
	for _, tc := range []struct {
		name   string
		remove func(p string) error
	}{
		{name: "Remove", remove: os.Remove},
		{name: "Rename", remove: func(p string) error { return os.Rename(p, p+".old") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			blobs := t.TempDir()
			b, desc := randomBlob(t, 1024)
			p, err := localBlobPath(blobs, desc.Digest)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, b, 0o644); err != nil {
				t.Fatal(err)
			}

			ra, err := openLocal(blobs, desc.Digest, desc.Size)
			if err != nil {
				t.Fatal(err)
			}
			defer ra.Close()

			if err := tc.remove(p); err != nil {
				t.Fatalf("%s while open: %v", tc.name, err)
			}

			// The open blob is still readable.
			got, err := io.ReadAll(ra.Reader())
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, b) {
				t.Error("unexpected content after the blob was removed")
			}
		})
	}
}
