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
	"archive/tar"
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/plugins/content/local"
	"github.com/google/uuid"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// superblockUUID is the offset of the volume UUID in an EROFS image: the
// superblock starts at 1024 and the UUID 48 bytes into it.
const superblockUUID = 1024 + 48

// writeLayer stores a one-file uncompressed tar layer and returns its
// descriptor.
func writeLayer(t *testing.T, cs content.Store) ocispec.Descriptor {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: "hello", Size: 5, Mode: 0644}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayer,
		Digest:    digest.FromBytes(buf.Bytes()),
		Size:      int64(buf.Len()),
	}
	if err := content.WriteBlob(context.Background(), cs, "layer", bytes.NewReader(buf.Bytes()), desc); err != nil {
		t.Fatal(err)
	}
	return desc
}

// convertUUID converts the layer with opts and returns the UUID recorded in
// the image superblock.
func convertUUID(t *testing.T, opts ...ConvertOpt) (uuid.UUID, ocispec.Descriptor) {
	t.Helper()
	cs, err := local.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	desc := writeLayer(t, cs)
	out := filepath.Join(t.TempDir(), "layer.erofs")
	if _, err := ConvertLayerToErofs(context.Background(), cs, desc, out, opts...); err != nil {
		t.Fatalf("ConvertLayerToErofs: %v", err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var u uuid.UUID
	copy(u[:], b[superblockUUID:])
	return u, desc
}

// TestConvertLayerToErofsDefaultUsesGo verifies that without mkfs options the
// layer is converted in Go, which records no volume UUID.
func TestConvertLayerToErofsDefaultUsesGo(t *testing.T) {
	if u, _ := convertUUID(t); u != uuid.Nil {
		t.Errorf("default conversion recorded UUID %s; want none from the Go converter", u)
	}
}

// TestConvertLayerToErofsMkfsOptions verifies that WithMkfsOptions and
// WithCompressors convert with mkfs.erofs, which records the UUID derived
// from the layer's diffID.
func TestConvertLayerToErofsMkfsOptions(t *testing.T) {
	if _, err := exec.LookPath("mkfs.erofs"); err != nil {
		t.Skip("mkfs.erofs not found in PATH")
	}
	for name, opt := range map[string]ConvertOpt{
		"mkfs-options": WithMkfsOptions([]string{"-T0"}),
		"compressors":  WithCompressors("lz4"),
	} {
		t.Run(name, func(t *testing.T) {
			got, desc := convertUUID(t, opt)
			want := uuid.NewSHA1(uuid.NameSpaceURL, []byte("erofs:blobs/"+desc.Digest))
			if got != want {
				t.Errorf("mkfs.erofs conversion recorded UUID %s, want %s", got, want)
			}
		})
	}
}

// TestConvertLayerToErofsMkfsMissing verifies that requesting mkfs.erofs when
// it is not installed fails the conversion instead of falling back to Go.
func TestConvertLayerToErofsMkfsMissing(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	cs, err := local.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	desc := writeLayer(t, cs)
	out := filepath.Join(t.TempDir(), "layer.erofs")
	if _, err := ConvertLayerToErofs(context.Background(), cs, desc, out, WithMkfsOptions([]string{"-T0"})); err == nil {
		t.Fatal("ConvertLayerToErofs with mkfs options succeeded without mkfs.erofs; want an error")
	}
}
