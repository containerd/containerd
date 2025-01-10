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

//nolint:golint
package fuzz

import (
	"bytes"
	"context"
	_ "crypto/sha256" // required by go-digest
	"os"
	"path/filepath"
	"testing"

	fuzz "github.com/AdaLogics/go-fuzz-headers"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images/archive"
	"github.com/containerd/containerd/v2/plugins/content/local"
)

// checkBlobPath performs some basic validation
func checkBlobPath(dgst digest.Digest, root string) error {
	if err := dgst.Validate(); err != nil {
		return err
	}
	path := filepath.Join(root, "blobs", dgst.Algorithm().String(), dgst.Encoded())
	_, err := os.Stat(path)
	if err != nil {
		return err
	}
	return nil
}

// generateBlobs is a helper function to create random blobs
func generateBlobs(f *fuzz.ConsumeFuzzer) (map[digest.Digest][]byte, error) {
	blobs := map[digest.Digest][]byte{}
	blobQty, err := f.GetInt()
	if err != nil {
		return blobs, err
	}
	maxsize := 4096
	nblobs := blobQty % maxsize

	for i := 0; i < nblobs; i++ {
		digestBytes, err := f.GetBytes()
		if err != nil {
			return blobs, err
		}

		dgst := digest.FromBytes(digestBytes)
		blobs[dgst] = digestBytes
	}

	return blobs, nil
}

// checkwrite is a wrapper around content.WriteBlob()
func checkWrite(ctx context.Context, cs content.Store, dgst digest.Digest, p []byte) (digest.Digest, error) {
	if err := content.WriteBlob(ctx, cs, dgst.String(), bytes.NewReader(p),
		ocispec.Descriptor{Size: int64(len(p)), Digest: dgst}); err != nil {
		return dgst, err
	}
	return dgst, nil
}

// populateBlobStore creates a bunch of blobs
func populateBlobStore(ctx context.Context, cs content.Store, f *fuzz.ConsumeFuzzer) (map[digest.Digest][]byte, error) {
	blobs, err := generateBlobs(f)
	if err != nil {
		return nil, err
	}

	for dgst, p := range blobs {
		_, err := checkWrite(ctx, cs, dgst, p)
		if err != nil {
			return blobs, err
		}
	}
	return blobs, nil
}

// FuzzCSWalk implements a fuzzer that targets contentStore.Walk()
func FuzzCSWalk(f *testing.F) {
	f.Fuzz(func(t *testing.T, data []byte) {
		ctx := context.Background()
		expected := map[digest.Digest]struct{}{}
		found := map[digest.Digest]struct{}{}
		tmpDir := t.TempDir()
		cs, err := local.NewStore(tmpDir)
		if err != nil {
			return
		}

		f := fuzz.NewConsumer(data)
		blobs, err := populateBlobStore(ctx, cs, f)
		if err != nil {
			return
		}

		for dgst := range blobs {
			expected[dgst] = struct{}{}
		}

		if err := cs.Walk(ctx, func(bi content.Info) error {
			found[bi.Digest] = struct{}{}
			err = checkBlobPath(bi.Digest, tmpDir)
			if err != nil {
				return err
			}
			return nil
		}); err != nil {
			return
		}

		require.Equal(t, expected, found)
	})
}

func FuzzArchiveExport(f *testing.F) {
	f.Fuzz(func(t *testing.T, data []byte) {
		f := fuzz.NewConsumer(data)
		manifest := ocispec.Descriptor{}
		err := f.GenerateStruct(&manifest)
		if err != nil {
			return
		}
		ctx := context.Background()
		tmpDir := t.TempDir()
		cs, err := local.NewStore(tmpDir)
		if err != nil {
			return
		}
		_, err = populateBlobStore(ctx, cs, f)
		if err != nil {
			return
		}

		// use `t.TempDir` to clean up temp dir and files
		w, err := os.CreateTemp(t.TempDir(), "fuzz-output-file")
		if err != nil {
			return
		}
		defer w.Close()
		_ = archive.Export(ctx, cs, w, archive.WithManifest(manifest, "name"))
	})
}
