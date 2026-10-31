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

package uncompress

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/plugins/content/local"
)

// sequentialStore returns readers which only support sequential reads.
type sequentialStore struct {
	content.Store
}

func (s sequentialStore) ReaderAt(ctx context.Context, desc ocispec.Descriptor) (content.ReaderAt, error) {
	ra, err := s.Store.ReaderAt(ctx, desc)
	if err != nil {
		return nil, err
	}
	return sequentialReaderAt{ra}, nil
}

// sequentialReaderAt fails ReadAt, so content can only be read through the
// reader returned by Reader, as content.NewReader does.
type sequentialReaderAt struct {
	content.ReaderAt
}

func (ra sequentialReaderAt) ReadAt([]byte, int64) (int, error) {
	return 0, errors.New("ReadAt called on a sequential reader")
}

func (ra sequentialReaderAt) Reader() io.Reader {
	return io.NewSectionReader(ra.ReaderAt, 0, ra.Size())
}

func TestLayerConvertFuncReadsSequentially(t *testing.T) {
	ctx := context.Background()
	cs, err := local.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	uncompressed := bytes.Repeat([]byte("layer content "), 4096)
	var compressed bytes.Buffer
	gw := gzip.NewWriter(&compressed)
	if _, err := gw.Write(uncompressed); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayerGzip,
		Digest:    digest.FromBytes(compressed.Bytes()),
		Size:      int64(compressed.Len()),
	}
	if err := content.WriteBlob(ctx, cs, "layer", bytes.NewReader(compressed.Bytes()), desc); err != nil {
		t.Fatal(err)
	}

	newDesc, err := LayerConvertFunc(ctx, sequentialStore{cs}, desc)
	if err != nil {
		t.Fatal(err)
	}
	if newDesc.MediaType != ocispec.MediaTypeImageLayer {
		t.Errorf("media type = %q, want %q", newDesc.MediaType, ocispec.MediaTypeImageLayer)
	}
	if want := digest.FromBytes(uncompressed); newDesc.Digest != want {
		t.Errorf("digest = %s, want %s", newDesc.Digest, want)
	}
}
