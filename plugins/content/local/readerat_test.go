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

package local

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/containerd/containerd/v2/core/content"
)

// The returned ReaderAt exposes the backing file's path, which consumers use
// for same-filesystem optimizations such as hardlinking (blobs are
// immutable). The erofs differ's link_blobs option relies on this.
func TestReaderAtName(t *testing.T) {
	ctx := context.Background()
	cs, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	blob := []byte("some blob")
	dgst := digest.FromBytes(blob)
	desc := ocispec.Descriptor{Size: int64(len(blob)), Digest: dgst}
	if err := content.WriteBlob(ctx, cs, dgst.String(), bytes.NewReader(blob), desc); err != nil {
		t.Fatal(err)
	}

	ra, err := cs.ReaderAt(ctx, desc)
	if err != nil {
		t.Fatal(err)
	}
	defer ra.Close()

	named, ok := ra.(interface{ Name() string })
	if !ok {
		t.Fatal("ReaderAt does not expose the backing file path")
	}
	got, err := os.ReadFile(named.Name())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, blob) {
		t.Fatal("backing file content differs from the blob")
	}
}
