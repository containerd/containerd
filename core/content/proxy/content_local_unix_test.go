//go:build unix

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
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestOpenLocalFIFO verifies that a FIFO with no writer in the blobs
// directory is rejected rather than blocking the open, which a plain
// os.Open would do until a writer appears.
func TestOpenLocalFIFO(t *testing.T) {
	root := t.TempDir()
	_, desc := randomBlob(t, 10)
	p, err := localBlobPath(root, desc.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(p, 0o644); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := openLocal(root, desc.Digest, desc.Size)
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected an error opening a FIFO")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("openLocal blocked on a FIFO with no writer")
	}
}
