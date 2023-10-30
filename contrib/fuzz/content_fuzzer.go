//go:build gofuzz

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
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	fuzz "github.com/AdaLogics/go-fuzz-headers"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/containerd/containerd/v2/content"
	"github.com/containerd/containerd/v2/content/local"
	"github.com/containerd/containerd/v2/images/archive"
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
func FuzzCSWalk(data []byte) int {
	ctx := context.Background()
	expected := map[digest.Digest]struct{}{}
	found := map[digest.Digest]struct{}{}
	tmpdir, err := os.MkdirTemp("", "fuzzing-")
	if err != nil {
		return 0
	}
	defer os.RemoveAll(tmpdir)
	cs, err := local.NewStore(tmpdir)
	if err != nil {
		return 0
	}

	f := fuzz.NewConsumer(data)
	blobs, err := populateBlobStore(ctx, cs, f)
	if err != nil {
		return 0
	}

	for dgst := range blobs {
		expected[dgst] = struct{}{}
	}

	if err := cs.Walk(ctx, func(bi content.Info) error {
		found[bi.Digest] = struct{}{}
		err = checkBlobPath(bi.Digest, tmpdir)
		if err != nil {
			return err
		}
		return nil
	}); err != nil {
		return 0
	}
	if !reflect.DeepEqual(expected, found) {
		panic(fmt.Sprintf("%v != %v but should be equal", found, expected))
	}
	return 1
}

func FuzzArchiveExport(data []byte) int {
	f := fuzz.NewConsumer(data)
	manifest := ocispec.Descriptor{}
	err := f.GenerateStruct(&manifest)
	if err != nil {
		return 0
	}
	ctx := context.Background()
	tmpdir, err := os.MkdirTemp("", "fuzzing-")
	if err != nil {
		return 0
	}
	defer os.RemoveAll(tmpdir)
	cs, err := local.NewStore(tmpdir)
	if err != nil {
		return 0
	}
	_, err = populateBlobStore(ctx, cs, f)
	if err != nil {
		return 0
	}
	w, err := os.Create("fuzz-output-file")
	if err != nil {
		return 0
	}
	defer w.Close()
	defer os.Remove("fuzz-output-file")
	_ = archive.Export(ctx, cs, w, archive.WithManifest(manifest, "name"))
	return 1
}
