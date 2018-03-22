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

package testsuite

import (
	"context"
	"io"
	"io/ioutil"
	"testing"

	"github.com/containerd/containerd/content"
	digest "github.com/opencontainers/go-digest"
)

// ProviderIngester consists of provider and ingester.
// This interface is used for test cases that does not require
// complete content.Store implementation.
type ProviderIngester interface {
	content.Provider
	content.Ingester
}

// TestSmallBlob tests reading a blob which is smaller than the read size.
// This test is exposed so that it can be used for incomplete content.Store implementation.
func TestSmallBlob(ctx context.Context, t *testing.T, store ProviderIngester) {
	blob := []byte(`foobar`)
	blobSize := int64(len(blob))
	blobDigest := digest.FromBytes(blob)
	// test write
	w, err := store.Writer(ctx, t.Name(), blobSize, blobDigest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(blob); err != nil {
		t.Fatal(err)
	}
	if err := w.Commit(ctx, blobSize, blobDigest); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	// test read.
	readSize := blobSize + 1
	ra, err := store.ReaderAt(ctx, blobDigest)
	if err != nil {
		t.Fatal(err)
	}
	r := io.NewSectionReader(ra, 0, readSize)
	b, err := ioutil.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := ra.Close(); err != nil {
		t.Fatal(err)
	}
	d := digest.FromBytes(b)
	if blobDigest != d {
		t.Fatalf("expected %s (%q), got %s (%q)", blobDigest, string(blob),
			d, string(b))
	}
}
