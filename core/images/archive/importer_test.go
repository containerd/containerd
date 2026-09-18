package archive

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/pkg/archive/compression"
	"github.com/containerd/containerd/v2/plugins/content/local"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// buildDockerArchive returns a Docker v1.2 tarball (manifest.json, no oci-layout)
// with a single one-file layer compressed with c.
func buildDockerArchive(t *testing.T, c compression.Compression) *bytes.Buffer {
	t.Helper()
	var layerTar bytes.Buffer
	tw := tar.NewWriter(&layerTar)
	tw.WriteHeader(&tar.Header{Name: "hello", Mode: 0644, Size: 5})
	tw.Write([]byte("hello"))
	tw.Close()
	diffID := digest.FromBytes(layerTar.Bytes())

	var layerBlob bytes.Buffer
	if c == compression.Uncompressed {
		layerBlob.Write(layerTar.Bytes())
	} else {
		zw, err := compression.CompressStream(&layerBlob, c)
		if err != nil {
			t.Fatal(err)
		}
		_, err = zw.Write([]byte("hello"))
		if err != nil {
			t.Fatal(err)
		}
		zw.Close()
	}

	cfg, _ := json.Marshal(ocispec.Image{
		Platform: ocispec.Platform{Architecture: "amd64", OS: "linux"},
		RootFS:   ocispec.RootFS{Type: "layers", DiffIDs: []digest.Digest{diffID}},
	})
	mfst, _ := json.Marshal([]struct {
		Config   string
		RepoTags []string
		Layers   []string
	}{{Config: "config.json", RepoTags: []string{"example.com/repro:latest"}, Layers: []string{"layer.tar"}}})

	var archiveBuf bytes.Buffer
	aw := tar.NewWriter(&archiveBuf)
	for _, f := range []struct {
		name string
		data []byte
	}{
		{"config.json", cfg},
		{"layer.tar", layerBlob.Bytes()},
		{"manifest.json", mfst},
	} {
		aw.WriteHeader(&tar.Header{Name: f.name, Mode: 0644, Size: int64(len(f.data))})
		aw.Write(f.data)
	}
	aw.Close()
	return &archiveBuf
}

func importedLayerMediaType(t *testing.T, store content.Store, buf *bytes.Buffer) string {
	t.Helper()
	ctx := context.Background()
	idxDesc, err := ImportIndex(ctx, store, buf)
	if err != nil {
		t.Fatal(err)
	}
	var idx ocispec.Index
	b, _ := content.ReadBlob(ctx, store, idxDesc)
	json.Unmarshal(b, &idx)
	var m ocispec.Manifest
	b, _ = content.ReadBlob(ctx, store, idx.Manifests[0])
	json.Unmarshal(b, &m)
	return m.Layers[0].MediaType
}

func TestImportLayerMediaType(t *testing.T) {
	for _, tc := range []struct {
		name string
		c    compression.Compression
		want string
	}{
		{"uncompressed", compression.Uncompressed, images.MediaTypeDockerSchema2Layer},
		{"gzip", compression.Gzip, images.MediaTypeDockerSchema2LayerGzip},
		{"zstd", compression.Zstd, images.MediaTypeDockerSchema2LayerZstd},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, err := local.NewStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if got := importedLayerMediaType(t, store, buildDockerArchive(t, tc.c)); got != tc.want {
				t.Errorf("first import (resolveLayers): media type = %q, want %q", got, tc.want)
			}
			// Importing again into the same store hits detectLayerMediaType.
			if got := importedLayerMediaType(t, store, buildDockerArchive(t, tc.c)); got != tc.want {
				t.Errorf("second import (detectLayerMediaType): media type = %q, want %q", got, tc.want)
			}
		})
	}
}
