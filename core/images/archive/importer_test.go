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
	"context"
	"testing"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/plugins/content/local"
	"github.com/containerd/errdefs"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// countingStore counts the writers opened for blobs not in the store yet.
type countingStore struct {
	content.Store
	writers int
}

func (s *countingStore) Writer(ctx context.Context, opts ...content.WriterOpt) (content.Writer, error) {
	w, err := s.Store.Writer(ctx, opts...)
	if err == nil {
		s.writers++
	}
	return w, err
}

func ociLayoutTar(t *testing.T, blobName string, blob []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, f := range []struct {
		name string
		data []byte
	}{
		{"oci-layout", []byte(`{"imageLayoutVersion":"1.0.0"}`)},
		{"index.json", []byte(`{"schemaVersion":2,"manifests":[]}`)},
		{blobName, blob},
	} {
		require.NoError(t, tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o644, Size: int64(len(f.data)), Typeflag: tar.TypeReg}))
		_, err := tw.Write(f.data)
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	return buf.Bytes()
}

func TestImportIndexSkipsExistingBlobs(t *testing.T) {
	ctx := context.Background()
	cs, err := local.NewStore(t.TempDir())
	require.NoError(t, err)
	store := &countingStore{Store: cs}
	blob := []byte("layer content")
	tarball := ociLayoutTar(t, "blobs/sha256/"+digest.FromBytes(blob).Encoded(), blob)

	_, err = ImportIndex(ctx, store, bytes.NewReader(tarball))
	require.NoError(t, err)
	assert.Equal(t, 2, store.writers, "first import writes index.json and the blob")

	store.writers = 0
	_, err = ImportIndex(ctx, store, bytes.NewReader(tarball))
	require.NoError(t, err)
	assert.Equal(t, 1, store.writers, "second import writes index.json and skips the existing blob")
}

func TestImportIndexRejectsBlobNotMatchingItsDigest(t *testing.T) {
	ctx := context.Background()
	cs, err := local.NewStore(t.TempDir())
	require.NoError(t, err)
	blob := []byte("layer content")
	name := "blobs/sha256/" + digest.FromBytes(blob).Encoded()

	_, err = ImportIndex(ctx, cs, bytes.NewReader(ociLayoutTar(t, name, []byte("layer CONTENT"))))
	assert.True(t, errdefs.IsFailedPrecondition(err), "expected a failed precondition error, got %v", err)

	// A valid import must not resume the rejected data.
	_, err = ImportIndex(ctx, cs, bytes.NewReader(ociLayoutTar(t, name, blob)))
	require.NoError(t, err)
	stored, err := content.ReadBlob(ctx, cs, ocispec.Descriptor{Digest: digest.FromBytes(blob), Size: int64(len(blob))})
	require.NoError(t, err)
	assert.Equal(t, blob, stored)
}

func TestBlobDigest(t *testing.T) {
	valid := digest.FromString("blob")
	for name, expected := range map[string]digest.Digest{
		"blobs/sha256/" + valid.Encoded():          valid,
		"blobs/sha256/" + valid.Encoded() + "/foo": "",
		"blobs/sha256/not-a-digest":                "",
		"blobs/unknown/" + valid.Encoded():         "",
		"blobs/sha256":                             "",
		"index.json":                               "",
		"abc123/layer.tar":                         "",
	} {
		assert.Equal(t, expected, blobDigest(name), name)
	}
}
