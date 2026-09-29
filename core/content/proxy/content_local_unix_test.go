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

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// openLocalWithin calls openLocal, failing the test if it blocks.
func openLocalWithin(t *testing.T, blobs string, desc ocispec.Descriptor) error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		ra, err := openLocal(blobs, desc.Digest, desc.Size)
		if err == nil {
			ra.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("openLocal blocked")
		return nil
	}
}

// TestOpenLocalFIFO verifies that a FIFO with no writer, at the blob's path or
// at the blobs directory's path, is rejected rather than blocking the open,
// which a plain os.Open would do until a writer appears.
func TestOpenLocalFIFO(t *testing.T) {
	_, desc := randomBlob(t, 10)

	t.Run("Blob", func(t *testing.T) {
		blobs := t.TempDir()
		p, err := localBlobPath(blobs, desc.Digest)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := syscall.Mkfifo(p, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := openLocalWithin(t, blobs, desc); err == nil {
			t.Error("expected an error opening a FIFO")
		}
	})

	t.Run("Directory", func(t *testing.T) {
		blobs := filepath.Join(t.TempDir(), "blobs")
		if err := syscall.Mkfifo(blobs, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := openLocalWithin(t, blobs, desc); err == nil {
			t.Error("expected an error opening under a FIFO")
		}
	})
}

// TestOpenLocalSymlinkOutside verifies that a blob which is a symlink to a file
// outside the blobs directory is not opened.
func TestOpenLocalSymlinkOutside(t *testing.T) {
	b, desc := randomBlob(t, 10)
	outside := filepath.Join(t.TempDir(), "blob")
	if err := os.WriteFile(outside, b, 0o644); err != nil {
		t.Fatal(err)
	}
	blobs := t.TempDir()
	p, err := localBlobPath(blobs, desc.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, p); err != nil {
		t.Fatal(err)
	}
	if err := openLocalWithin(t, blobs, desc); err == nil {
		t.Error("expected an error opening a symlink out of the blobs directory")
	}
}
