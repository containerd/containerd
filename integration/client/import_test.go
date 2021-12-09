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

package client

import (
	"bytes"
	"context"
	"encoding/json"
	"io"

	"math/rand"
	"reflect"
	"runtime"
	"testing"
	"time"

	. "github.com/containerd/containerd"
	"github.com/containerd/containerd/archive/compression"
	"github.com/containerd/containerd/archive/tartest"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/images/archive"
	"github.com/containerd/containerd/leases"
	"github.com/containerd/containerd/oci"
	"github.com/containerd/containerd/platforms"

	digest "github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// TestExportAndImport exports testImage as a tar stream,
// and import the tar stream as a new image.
func TestExportAndImport(t *testing.T) {
	testExportImport(t, testImage)
}

// TestExportAndImportMultiLayer exports testMultiLayeredImage as a tar stream,
// and import the tar stream as a new image. This should ensure that imported
// images remain sane, and that the Garbage Collector won't delete part of its
// content.
func TestExportAndImportMultiLayer(t *testing.T) {
	testExportImport(t, testMultiLayeredImage)
}

func testExportImport(t *testing.T, imageName string) {
	if testing.Short() {
		t.Skip()
	}
	ctx, cancel := testContext(t)
	defer cancel()

	client, err := New(address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	_, err = client.Fetch(ctx, imageName)
	if err != nil {
		t.Fatal(err)
	}

	wb := bytes.NewBuffer(nil)
	err = client.Export(ctx, wb, archive.WithPlatform(platforms.Default()), archive.WithImage(client.ImageService(), imageName))
	if err != nil {
		t.Fatal(err)
	}

	client.ImageService().Delete(ctx, imageName)

	opts := []ImportOpt{
		WithImageRefTranslator(archive.AddRefPrefix("foo/bar")),
	}
	imgrecs, err := client.Import(ctx, bytes.NewReader(wb.Bytes()), opts...)
	if err != nil {
		t.Fatalf("Import failed: %+v", err)
	}

	// We need to unpack the image, especially if it's multilayered.
	for _, img := range imgrecs {
		image := NewImage(client, img)

		// TODO: Show unpack status
		t.Logf("unpacking %s (%s)...", img.Name, img.Target.Digest)
		err = image.Unpack(ctx, "")
		if err != nil {
			t.Fatalf("Error while unpacking image: %+v", err)
		}
		t.Log("done")
	}

	// we're triggering the Garbage Collector to do its job.
	ls := client.LeasesService()
	l, err := ls.Create(ctx, leases.WithRandomID(), leases.WithExpiration(time.Hour))
	if err != nil {
		t.Fatalf("Error while creating lease: %+v", err)
	}
	if err = ls.Delete(ctx, l, leases.SynchronousDelete); err != nil {
		t.Fatalf("Error while deleting lease: %+v", err)
	}

	image, err := client.GetImage(ctx, imageName)
	if err != nil {
		t.Fatal(err)
	}

	id := t.Name()
	container, err := client.NewContainer(ctx, id, WithNewSnapshot(id, image), WithNewSpec(oci.WithImageConfig(image)))
	if err != nil {
		t.Fatalf("Error while creating container: %+v", err)
	}
	container.Delete(ctx, WithSnapshotCleanup)

	for _, imgrec := range imgrecs {
		if imgrec.Name == testImage {
			continue
		}
		err = client.ImageService().Delete(ctx, imgrec.Name)
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestImport(t *testing.T) {
	ctx, cancel := testContext(t)
	defer cancel()

	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	tc := tartest.TarContext{}

	b1, d1 := createContent(256, 1)
	empty := []byte("{}")
	version := []byte("1.0")

	c1, d2 := createConfig(runtime.GOOS, runtime.GOARCH)
	badConfig, _ := createConfig("foo", "lish")

	m1, d3, expManifest := createManifest(c1, [][]byte{b1})

	provider := client.ContentStore()

	checkManifest := func(ctx context.Context, t *testing.T, d ocispec.Descriptor, expManifest *ocispec.Manifest) {
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

		if expManifest != nil {
			if !reflect.DeepEqual(m.Layers, expManifest.Layers) {
				t.Fatalf("DeepEqual on Layers failed: %v vs. %v", m.Layers, expManifest.Layers)
			}
			if !reflect.DeepEqual(m.Config, expManifest.Config) {
				t.Fatalf("DeepEqual on Config failed: %v vs. %v", m.Config, expManifest.Config)
			}
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
				checkManifest(ctx, t, imgs[0].Target, nil)
			},
		},
		{
			Name: "DockerV2.1-BadOSArch",
			Writer: tartest.TarAll(
				tc.Dir("bd765cd43e95212f7aa2cab51d0a", 0755),
				tc.File("bd765cd43e95212f7aa2cab51d0a/json", empty, 0644),
				tc.File("bd765cd43e95212f7aa2cab51d0a/layer.tar", b1, 0644),
				tc.File("bd765cd43e95212f7aa2cab51d0a/VERSION", version, 0644),
				tc.File("e95212f7aa2cab51d0abd765cd43.json", badConfig, 0644),
				tc.File("manifest.json", []byte(`[{"Config":"e95212f7aa2cab51d0abd765cd43.json","RepoTags":["test-import:notlatest", "another/repo:tag"],"Layers":["bd765cd43e95212f7aa2cab51d0a/layer.tar"]}]`), 0644),
			),
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
				checkManifest(ctx, t, imgs[0].Target, expManifest)
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
				checkManifest(ctx, t, imgs[0].Target, expManifest)
			},
			Opts: []ImportOpt{
				WithImageRefTranslator(archive.AddRefPrefix("localhost:5000/myimage")),
			},
		},
		{
			Name: "OCIPrefixName2",
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
				checkManifest(ctx, t, imgs[0].Target, expManifest)
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

		if actual[i].Target.MediaType != ocispec.MediaTypeImageManifest &&
			actual[i].Target.MediaType != images.MediaTypeDockerSchema2Manifest {
			t.Fatalf("image(%d) unexpected media type: %s", i, actual[i].Target.MediaType)
		}
	}
}

func createContent(size int64, seed int64) ([]byte, digest.Digest) {
	b, err := io.ReadAll(io.LimitReader(rand.New(rand.NewSource(seed)), size))
	if err != nil {
		panic(err)
	}
	wb := bytes.NewBuffer(nil)
	cw, err := compression.CompressStream(wb, compression.Gzip)
	if err != nil {
		panic(err)
	}

	if _, err := cw.Write(b); err != nil {
		panic(err)
	}
	b = wb.Bytes()
	return b, digest.FromBytes(b)
}

func createConfig(osName, archName string) ([]byte, digest.Digest) {
	image := ocispec.Image{
		OS:           osName,
		Architecture: archName,
		Author:       "test",
	}
	b, _ := json.Marshal(image)

	return b, digest.FromBytes(b)
}

func createManifest(config []byte, layers [][]byte) ([]byte, digest.Digest, *ocispec.Manifest) {
	manifest := ocispec.Manifest{
		Versioned: specs.Versioned{
			SchemaVersion: 2,
		},
		Config: ocispec.Descriptor{
			MediaType: ocispec.MediaTypeImageConfig,
			Digest:    digest.FromBytes(config),
			Size:      int64(len(config)),
			Annotations: map[string]string{
				"ocispec": "manifest.config.descriptor",
			},
		},
	}
	for _, l := range layers {
		manifest.Layers = append(manifest.Layers, ocispec.Descriptor{
			MediaType: ocispec.MediaTypeImageLayer,
			Digest:    digest.FromBytes(l),
			Size:      int64(len(l)),
			Annotations: map[string]string{
				"ocispec": "manifest.layers.descriptor",
			},
		})
	}

	b, _ := json.Marshal(manifest)

	return b, digest.FromBytes(b), &manifest
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
