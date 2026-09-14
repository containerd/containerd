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

package erofs

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/plugins/content/local"
)

// applyNativeLayer runs Apply for an uncompressed native EROFS layer blob
// (the fastcopy path) and returns the store blob path and the applied layer
// path.
func applyNativeLayer(t *testing.T, opts ...DifferOpt) (blobPath, layerBlobPath string) {
	t.Helper()
	ctx := context.Background()

	store, err := local.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	blob := []byte("not a real erofs image, fastcopy does not parse it")
	dgst := digest.FromBytes(blob)
	if err := content.WriteBlob(ctx, store, dgst.String(), bytes.NewReader(blob),
		ocispec.Descriptor{Size: int64(len(blob)), Digest: dgst}); err != nil {
		t.Fatal(err)
	}

	// Recover the backing file path the same way Apply does.
	ra, err := store.ReaderAt(ctx, ocispec.Descriptor{Digest: dgst, Size: int64(len(blob))})
	if err != nil {
		t.Fatal(err)
	}
	defer ra.Close()
	named, ok := ra.(interface{ Name() string })
	if !ok {
		t.Fatal("local store ReaderAt does not expose the backing file path")
	}
	blobPath = named.Name()

	layerDir := t.TempDir()
	// Mark the directory as an erofs snapshotter layer, as MountsToLayer requires.
	if err := os.WriteFile(filepath.Join(layerDir, ".erofslayer"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	desc := ocispec.Descriptor{
		MediaType: images.MediaTypeErofsLayer,
		Digest:    dgst,
		Size:      int64(len(blob)),
	}
	mounts := []mount.Mount{{Type: "bind", Source: filepath.Join(layerDir, "fs")}}

	d := NewErofsDiffer(store, opts...)
	if _, err := d.Apply(ctx, desc, mounts); err != nil {
		t.Fatal(err)
	}

	layerBlobPath = filepath.Join(layerDir, "layer.erofs")
	got, err := os.ReadFile(layerBlobPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, blob) {
		t.Fatal("applied layer content differs from the blob")
	}
	return blobPath, layerBlobPath
}

func sameFile(t *testing.T, a, b string) bool {
	t.Helper()
	fa, err := os.Stat(a)
	if err != nil {
		t.Fatal(err)
	}
	fb, err := os.Stat(b)
	if err != nil {
		t.Fatal(err)
	}
	return os.SameFile(fa, fb)
}

func TestApplyLinkBlobs(t *testing.T) {
	blobPath, layerBlobPath := applyNativeLayer(t, WithBlobLinks())
	if !sameFile(t, blobPath, layerBlobPath) {
		t.Error("expected the applied layer to be hardlinked from the content store blob")
	}
}

func TestApplyLinkBlobsDisabled(t *testing.T) {
	blobPath, layerBlobPath := applyNativeLayer(t)
	if sameFile(t, blobPath, layerBlobPath) {
		t.Error("expected the applied layer to be a copy, not a hardlink")
	}
}

// Layers formatted with dm-verity are appended to after apply; the blob's
// inode must never be shared in that configuration.
func TestApplyLinkBlobsDmverityNeverLinks(t *testing.T) {
	blobPath, layerBlobPath := applyNativeLayer(t, WithBlobLinks(), WithDmverity())
	if sameFile(t, blobPath, layerBlobPath) {
		t.Error("expected the applied layer to be a copy when dm-verity is enabled")
	}
}
