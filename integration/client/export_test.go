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
	"archive/tar"
	"context"
	"encoding/json"
	"io"
	"os"
	"testing"

	. "github.com/containerd/containerd/v2"
	"github.com/containerd/containerd/v2/content"
	"github.com/containerd/containerd/v2/images"
	"github.com/containerd/containerd/v2/images/archive"
	"github.com/containerd/containerd/v2/platforms"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// TestExport exports testImage as a tar stream
func TestExport(t *testing.T) {
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

	_, err = client.Fetch(ctx, testImage)
	if err != nil {
		t.Fatal(err)
	}
	dstFile, err := os.CreateTemp("", "export-import-test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		dstFile.Close()
		os.Remove(dstFile.Name())
	}()

	err = client.Export(ctx, dstFile, archive.WithPlatform(platforms.Default()), archive.WithImage(client.ImageService(), testImage))
	if err != nil {
		t.Fatal(err)
	}

	// Seek to beginning of file before passing it to assertOCITar()
	dstFile.Seek(0, 0)
	assertOCITar(t, dstFile, true)
}

// TestExportDockerManifest exports testImage as a tar stream, using the
// WithSkipDockerManifest option
func TestExportDockerManifest(t *testing.T) {
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

	_, err = client.Fetch(ctx, testImage)
	if err != nil {
		t.Fatal(err)
	}
	dstFile, err := os.CreateTemp("", "export-import-test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		dstFile.Close()
		os.Remove(dstFile.Name())
	}()

	img, err := client.ImageService().Get(ctx, testImage)
	if err != nil {
		t.Fatal(err)
	}

	// test multi-platform export
	err = client.Export(ctx, dstFile, archive.WithManifest(img.Target), archive.WithSkipDockerManifest())
	if err != nil {
		t.Fatal(err)
	}
	dstFile.Seek(0, 0)
	assertOCITar(t, dstFile, false)

	// reset to beginning
	dstFile.Seek(0, 0)

	// test single-platform export
	var result ocispec.Descriptor
	err = images.Walk(ctx, images.HandlerFunc(func(ctx context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
		if images.IsManifestType(desc.MediaType) {
			p, err := content.ReadBlob(ctx, client.ContentStore(), desc)
			if err != nil {
				return nil, err
			}

			var manifest ocispec.Manifest
			if err := json.Unmarshal(p, &manifest); err != nil {
				return nil, err
			}

			if desc.Platform == nil || platforms.Default().Match(platforms.Normalize(*desc.Platform)) {
				result = desc
			}
			return nil, nil
		} else if images.IsIndexType(desc.MediaType) {
			p, err := content.ReadBlob(ctx, client.ContentStore(), desc)
			if err != nil {
				return nil, err
			}

			var idx ocispec.Index
			if err := json.Unmarshal(p, &idx); err != nil {
				return nil, err
			}
			return idx.Manifests, nil
		}
		return nil, nil
	}), img.Target)
	if err != nil {
		t.Fatal(err)
	}
	err = client.Export(ctx, dstFile, archive.WithManifest(result), archive.WithSkipDockerManifest())
	if err != nil {
		t.Fatal(err)
	}
	dstFile.Seek(0, 0)
	assertOCITar(t, dstFile, false)
}

func assertOCITar(t *testing.T, r io.Reader, docker bool) {
	// TODO: add more assertion
	tr := tar.NewReader(r)
	foundOCILayout := false
	foundIndexJSON := false
	foundManifestJSON := false
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Error(err)
			continue
		}
		if h.Name == ocispec.ImageLayoutFile {
			foundOCILayout = true
		}
		if h.Name == ocispec.ImageIndexFile {
			foundIndexJSON = true
		}
		if h.Name == "manifest.json" {
			foundManifestJSON = true
		}
	}
	if !foundOCILayout {
		t.Error("oci-layout not found")
	}
	if !foundIndexJSON {
		t.Error("index.json not found")
	}
	if docker && !foundManifestJSON {
		t.Error("manifest.json not found")
	} else if !docker && foundManifestJSON {
		t.Error("manifest.json found")
	}
}
