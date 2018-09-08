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

package containerd

import (
	"context"
	"encoding/json"
	"io"

	"io/ioutil"
	"math/rand"
	"runtime"
	"testing"

	"github.com/containerd/containerd/archive/tartest"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/images/archive"
	digest "github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// TestOCIExportAndImport exports testImage as a tar stream,
// and import the tar stream as a new image.
func TestOCIExportAndImport(t *testing.T) {
	// TODO: support windows
	if testing.Short() || runtime.GOOS == "windows" {
		t.Skip()
	}
	ctx, cancel := testContext()
	defer cancel()

	client, err := New(address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	pulled, err := client.Fetch(ctx, testImage)
	if err != nil {
		t.Fatal(err)
	}

	exported, err := client.Export(ctx, pulled.Target)
	if err != nil {
		t.Fatal(err)
	}

	opts := []ImportOpt{
		WithImageRefTranslator(archive.AddRefPrefix("foo/bar")),
	}
	imgrecs, err := client.Import(ctx, exported, opts...)
	if err != nil {
		t.Fatalf("Import failed: %+v", err)
	}

	for _, imgrec := range imgrecs {
		err = client.ImageService().Delete(ctx, imgrec.Name)
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestImport(t *testing.T) {
	ctx, cancel := testContext()
	defer cancel()

	client, err := New(address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	tc := tartest.TarContext{}

	b1, d1 := createContent(256, 1)
	empty := []byte("{}")
	version := []byte("1.0")

	c1, d2 := createConfig()

	m1, d3 := createManifest(c1, [][]byte{b1})

	provider := client.ContentStore()

	checkManifest := func(ctx context.Context, t *testing.T, d ocispec.Descriptor) {
		m, err := images.Manifest(ctx, provider, d, nil)
		if err != nil {
			t.Fatalf("unable to read target blob: %+v", err)
		}

		if m.Config.Digest != d2 {
			t.Fatalf("unexpected digest hash %s, expected %s", m.Config.Digest, d2)
		}

		if len(m.Layers) != 1 {
			t.Fatalf("expected 1 layer, has %d", len(m.Layers))
		}

		if m.Layers[0].Digest != d1 {
			t.Fatalf("unexpected layer hash %s, expected %s", m.Layers[0].Digest, d1)
		}
	}

	for _, tc := range []struct {
		Name   string
		Writer tartest.WriterToTar
		Check  func(*testing.T, []images.Image)
		Opts   []ImportOpt
	}{
		{
			Name: "DockerV2.0",
			Writer: tartest.TarAll(
				tc.Dir("bd765cd43e95212f7aa2cab51d0a", 0755),
				tc.File("bd765cd43e95212f7aa2cab51d0a/json", empty, 0644),
				tc.File("bd765cd43e95212f7aa2cab51d0a/layer.tar", b1, 0644),
				tc.File("bd765cd43e95212f7aa2cab51d0a/VERSION", version, 0644),
				tc.File("repositories", []byte(`{"any":{"1":"bd765cd43e95212f7aa2cab51d0a"}}`), 0644),
			),
		},
		{
			Name: "DockerV2.1",
			Writer: tartest.TarAll(
				tc.Dir("bd765cd43e95212f7aa2cab51d0a", 0755),
				tc.File("bd765cd43e95212f7aa2cab51d0a/json", empty, 0644),
				tc.File("bd765cd43e95212f7aa2cab51d0a/layer.tar", b1, 0644),
				tc.File("bd765cd43e95212f7aa2cab51d0a/VERSION", version, 0644),
				tc.File("e95212f7aa2cab51d0abd765cd43.json", c1, 0644),
				tc.File("manifest.json", []byte(`[{"Config":"e95212f7aa2cab51d0abd765cd43.json","RepoTags":["test-import:notlatest", "another/repo:tag"],"Layers":["bd765cd43e95212f7aa2cab51d0a/layer.tar"]}]`), 0644),
			),
			Check: func(t *testing.T, imgs []images.Image) {
				if len(imgs) == 0 {
					t.Fatalf("no images")
				}

				names := []string{
					"docker.io/library/test-import:notlatest",
					"docker.io/another/repo:tag",
				}

				checkImages(t, imgs[0].Target.Digest, imgs, names...)
				checkManifest(ctx, t, imgs[0].Target)
			},
		},
		{
			Name: "OCI-BadFormat",
			Writer: tartest.TarAll(
				tc.File("oci-layout", []byte(`{"imageLayoutVersion":"2.0.0"}`), 0644),
			),
		},
		{
			Name: "OCI",
			Writer: tartest.TarAll(
				tc.Dir("blobs", 0755),
				tc.Dir("blobs/sha256", 0755),
				tc.File("blobs/sha256/"+d1.Encoded(), b1, 0644),
				tc.File("blobs/sha256/"+d2.Encoded(), c1, 0644),
				tc.File("blobs/sha256/"+d3.Encoded(), m1, 0644),
				tc.File("index.json", createIndex(m1, "latest", "docker.io/lib/img:ok"), 0644),
				tc.File("oci-layout", []byte(`{"imageLayoutVersion":"1.0.0"}`), 0644),
			),
			Check: func(t *testing.T, imgs []images.Image) {
				names := []string{
					"latest",
					"docker.io/lib/img:ok",
				}

				checkImages(t, d3, imgs, names...)
				checkManifest(ctx, t, imgs[0].Target)
			},
		},
		{
			Name: "OCIPrefixName",
			Writer: tartest.TarAll(
				tc.Dir("blobs", 0755),
				tc.Dir("blobs/sha256", 0755),
				tc.File("blobs/sha256/"+d1.Encoded(), b1, 0644),
				tc.File("blobs/sha256/"+d2.Encoded(), c1, 0644),
				tc.File("blobs/sha256/"+d3.Encoded(), m1, 0644),
				tc.File("index.json", createIndex(m1, "latest", "docker.io/lib/img:ok"), 0644),
				tc.File("oci-layout", []byte(`{"imageLayoutVersion":"1.0.0"}`), 0644),
			),
			Check: func(t *testing.T, imgs []images.Image) {
				names := []string{
					"localhost:5000/myimage:latest",
					"docker.io/lib/img:ok",
				}

				checkImages(t, d3, imgs, names...)
				checkManifest(ctx, t, imgs[0].Target)
			},
			Opts: []ImportOpt{
				WithImageRefTranslator(archive.AddRefPrefix("localhost:5000/myimage")),
			},
		},
		{
			Name: "OCIPrefixName",
			Writer: tartest.TarAll(
				tc.Dir("blobs", 0755),
				tc.Dir("blobs/sha256", 0755),
				tc.File("blobs/sha256/"+d1.Encoded(), b1, 0644),
				tc.File("blobs/sha256/"+d2.Encoded(), c1, 0644),
				tc.File("blobs/sha256/"+d3.Encoded(), m1, 0644),
				tc.File("index.json", createIndex(m1, "latest", "localhost:5000/myimage:old", "docker.io/lib/img:ok"), 0644),
				tc.File("oci-layout", []byte(`{"imageLayoutVersion":"1.0.0"}`), 0644),
			),
			Check: func(t *testing.T, imgs []images.Image) {
				names := []string{
					"localhost:5000/myimage:latest",
					"localhost:5000/myimage:old",
				}

				checkImages(t, d3, imgs, names...)
				checkManifest(ctx, t, imgs[0].Target)
			},
			Opts: []ImportOpt{
				WithImageRefTranslator(archive.FilterRefPrefix("localhost:5000/myimage")),
			},
		},
	} {
		t.Run(tc.Name, func(t *testing.T) {
			images, err := client.Import(ctx, tartest.TarFromWriterTo(tc.Writer), tc.Opts...)
			if err != nil {
				if tc.Check != nil {
					t.Errorf("unexpected import error: %+v", err)
				}
				return
			} else if tc.Check == nil {
				t.Fatalf("expected error on import")
			}

			tc.Check(t, images)
		})
	}
}

func checkImages(t *testing.T, target digest.Digest, actual []images.Image, names ...string) {
	if len(names) != len(actual) {
		t.Fatalf("expected %d images, got %d", len(names), len(actual))
	}

	for i, n := range names {
		if actual[i].Target.Digest != target {
			t.Fatalf("image(%d) unexpected target %s, expected %s", i, actual[i].Target.Digest, target)
		}
		if actual[i].Name != n {
			t.Fatalf("image(%d) unexpected name %q, expected %q", i, actual[i].Name, n)
		}

		if actual[i].Target.MediaType != ocispec.MediaTypeImageManifest {
			t.Fatalf("image(%d) unexpected media type: %s", i, actual[i].Target.MediaType)
		}
	}

}

func createContent(size int64, seed int64) ([]byte, digest.Digest) {
	b, err := ioutil.ReadAll(io.LimitReader(rand.New(rand.NewSource(seed)), size))
	if err != nil {
		panic(err)
	}
	return b, digest.FromBytes(b)
}

func createConfig() ([]byte, digest.Digest) {
	image := ocispec.Image{
		OS:           "any",
		Architecture: "any",
		Author:       "test",
	}
	b, _ := json.Marshal(image)

	return b, digest.FromBytes(b)
}

func createManifest(config []byte, layers [][]byte) ([]byte, digest.Digest) {
	manifest := ocispec.Manifest{
		Versioned: specs.Versioned{
			SchemaVersion: 2,
		},
		Config: ocispec.Descriptor{
			MediaType: ocispec.MediaTypeImageConfig,
			Digest:    digest.FromBytes(config),
			Size:      int64(len(config)),
		},
	}
	for _, l := range layers {
		manifest.Layers = append(manifest.Layers, ocispec.Descriptor{
			MediaType: ocispec.MediaTypeImageLayer,
			Digest:    digest.FromBytes(l),
			Size:      int64(len(l)),
		})
	}

	b, _ := json.Marshal(manifest)

	return b, digest.FromBytes(b)
}

func createIndex(manifest []byte, tags ...string) []byte {
	idx := ocispec.Index{
		Versioned: specs.Versioned{
			SchemaVersion: 2,
		},
	}
	d := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(manifest),
		Size:      int64(len(manifest)),
	}

	if len(tags) == 0 {
		idx.Manifests = append(idx.Manifests, d)
	} else {
		for _, t := range tags {
			dt := d
			dt.Annotations = map[string]string{
				ocispec.AnnotationRefName: t,
			}
			idx.Manifests = append(idx.Manifests, dt)
		}
	}

	b, _ := json.Marshal(idx)

	return b
}
