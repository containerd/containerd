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
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"

	. "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/images/archive"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/errdefs"
	"github.com/containerd/platforms"
	"github.com/google/uuid"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

func TestExportAllCases(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}

	for _, tc := range []struct {
		name    string
		prepare func(context.Context, *testing.T, *Client) images.Image
		check   func(context.Context, *testing.T, *Client, *os.File, images.Image)
	}{
		{
			name: "export all platforms without SkipMissing",
			prepare: func(ctx context.Context, t *testing.T, client *Client) images.Image {
				if runtime.GOOS == "windows" {
					t.Skip("skipping test on windows - the testimage index has only one platform")
				}
				img, err := client.Fetch(ctx, testImage, WithPlatform(platforms.DefaultString()), WithAllMetadata())
				if err != nil {
					t.Fatal(err)
				}
				return img
			},
			check: func(ctx context.Context, t *testing.T, client *Client, dstFile *os.File, _ images.Image) {
				err := client.Export(ctx, dstFile, archive.WithImage(client.ImageService(), testImage), archive.WithPlatform(platforms.All))
				if !errdefs.IsNotFound(err) {
					t.Fatal("should fail with not found error")
				}
			},
		},
		{
			name: "export all platforms with SkipMissing",
			prepare: func(ctx context.Context, t *testing.T, client *Client) images.Image {
				if runtime.GOOS == "windows" {
					t.Skip("skipping test on windows - the testimage index has only one platform")
				}
				img, err := client.Fetch(ctx, testImage, WithPlatform(platforms.DefaultString()), WithAllMetadata())
				if err != nil {
					t.Fatal(err)
				}
				return img
			},
			check: func(ctx context.Context, t *testing.T, client *Client, dstFile *os.File, img images.Image) {
				defaultPlatformManifest, err := getPlatformManifest(ctx, client.ContentStore(), img.Target, platforms.Default())
				if err != nil {
					t.Fatal(err)
				}
				err = client.Export(ctx, dstFile, archive.WithImage(client.ImageService(), testImage), archive.WithPlatform(platforms.All), archive.WithSkipMissing(client.ContentStore()))
				if err != nil {
					t.Fatal(err)
				}
				dstFile.Seek(0, 0)
				assertOCITar(t, dstFile, true)

				// Check if archive contains only one manifest for the default platform
				if !isImageInArchive(ctx, t, client, dstFile, defaultPlatformManifest) {
					t.Fatal("archive does not contain manifest for the default platform")
				}

				if isImageInArchive(ctx, t, client, dstFile, img.Target) {
					t.Fatal("archive shouldn't contain all platforms")
				}
			},
		},
		{
			name: "export full image",
			prepare: func(ctx context.Context, t *testing.T, client *Client) images.Image {
				img, err := client.Fetch(ctx, testImage)
				if err != nil {
					t.Fatal(err)
				}
				return img
			},
			check: func(ctx context.Context, t *testing.T, client *Client, dstFile *os.File, img images.Image) {
				err := client.Export(ctx, dstFile, archive.WithImage(client.ImageService(), testImage), archive.WithPlatform(platforms.All))
				if err != nil {
					t.Fatal(err)
				}

				// Seek to beginning of file before passing it to assertOCITar()
				dstFile.Seek(0, 0)
				assertOCITar(t, dstFile, true)

				// Archive should contain all platforms.
				if !isImageInArchive(ctx, t, client, dstFile, img.Target) {
					t.Fatalf("archive does not contain all platforms")
				}
			},
		},
		{
			name: "export multi-platform with SkipDockerManifest",
			prepare: func(ctx context.Context, t *testing.T, client *Client) images.Image {
				img, err := client.Fetch(ctx, testImage)
				if err != nil {
					t.Fatal(err)
				}
				return img
			},
			check: func(ctx context.Context, t *testing.T, client *Client, dstFile *os.File, img images.Image) {
				err := client.Export(ctx, dstFile, archive.WithImage(client.ImageService(), testImage), archive.WithManifest(img.Target), archive.WithSkipDockerManifest())
				if err != nil {
					t.Fatal(err)
				}

				// Seek to beginning of file before passing it to assertOCITar()
				dstFile.Seek(0, 0)
				assertOCITar(t, dstFile, false)

				if !isImageInArchive(ctx, t, client, dstFile, img.Target) {
					t.Fatalf("archive does not contain expected platform")
				}
			},
		},
		{
			name: "export single-platform with SkipDockerManifest",
			prepare: func(ctx context.Context, t *testing.T, client *Client) images.Image {
				img, err := client.Fetch(ctx, testImage, WithPlatform(platforms.DefaultString()))
				if err != nil {
					t.Fatal(err)
				}
				return img
			},
			check: func(ctx context.Context, t *testing.T, client *Client, dstFile *os.File, img images.Image) {
				result, err := getPlatformManifest(ctx, client.ContentStore(), img.Target, platforms.Default())
				if err != nil {
					t.Fatal(err)
				}

				err = client.Export(ctx, dstFile, archive.WithManifest(result), archive.WithSkipDockerManifest())
				if err != nil {
					t.Fatal(err)
				}

				// Seek to beginning of file before passing it to assertOCITar()
				dstFile.Seek(0, 0)
				assertOCITar(t, dstFile, false)

				if !isImageInArchive(ctx, t, client, dstFile, result) {
					t.Fatalf("archive does not contain expected platform")
				}
			},
		},
		{
			name: "export index only",
			prepare: func(ctx context.Context, t *testing.T, client *Client) images.Image {
				img, err := client.Fetch(ctx, testImage, WithPlatform(platforms.DefaultString()))
				if err != nil {
					t.Fatal(err)
				}

				var all []ocispec.Descriptor
				err = images.Walk(ctx, images.HandlerFunc(func(ctx context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
					ch, err := images.Children(ctx, client.ContentStore(), desc)
					if err != nil {
						if errdefs.IsNotFound(err) {
							return nil, images.ErrSkipDesc
						}
						return nil, err
					}
					all = append(all, ch...)

					return ch, nil
				}), img.Target)
				if err != nil {
					t.Fatal(err)
				}
				for _, d := range all {
					if images.IsIndexType(d.MediaType) {
						continue
					}
					if err := client.ContentStore().Delete(ctx, d.Digest); err != nil && !errdefs.IsNotFound(err) {
						t.Fatalf("failed to delete %v: %v", d.Digest, err)
					}
				}

				return img
			},
			check: func(ctx context.Context, t *testing.T, client *Client, dstFile *os.File, img images.Image) {
				err := client.Export(ctx, dstFile, archive.WithImage(client.ImageService(), testImage), archive.WithSkipMissing(client.ContentStore()))
				if err != nil {
					t.Fatal(err)
				}

				// Seek to beginning of file before passing it to assertOCITar()
				dstFile.Seek(0, 0)
				assertOCITar(t, dstFile, false)

				defaultPlatformManifest, err := getPlatformManifest(ctx, client.ContentStore(), img.Target, platforms.Default())
				if err != nil {
					t.Fatal(err)
				}

				// Check if archive contains only one manifest for the default platform
				if isImageInArchive(ctx, t, client, dstFile, defaultPlatformManifest) {
					t.Fatal("archive shouldn't contain manifest for the default platform")
				}
			},
		},
		{
			name: "export image setting ocispec.AnnotationRefName",
			prepare: func(ctx context.Context, t *testing.T, client *Client) images.Image {
				img, err := client.Fetch(ctx, testImageByDigest)
				if err != nil {
					t.Fatal(err)
				}
				return img
			},
			check: func(ctx context.Context, t *testing.T, client *Client, dstFile *os.File, img images.Image) {
				if err := client.Export(ctx, dstFile, archive.WithImage(client.ImageService(), testImageByDigest), archive.WithPlatform(platforms.All)); err != nil {
					t.Fatal(err)
				}

				if _, err := dstFile.Seek(0, io.SeekStart); err != nil {
					t.Fatal(err)
				}
				assertOCIIndexAnnotationRefName(t, dstFile)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := testContext(t)
			defer cancel()

			namespace := uuid.New().String()
			client, err := newClient(t, address, WithDefaultNamespace(namespace))
			require.NoError(t, err)
			t.Cleanup(func() {
				client.Close()
			})

			ctx = namespaces.WithNamespace(ctx, namespace)

			img := tc.prepare(ctx, t, client)

			dstFile, err := os.Create(filepath.Join(t.TempDir(), "export-test"))
			require.NoError(t, err)
			t.Cleanup(func() {
				dstFile.Close()
			})

			tc.check(ctx, t, client, dstFile, img)
		})
	}
}

func isImageInArchive(ctx context.Context, t *testing.T, client *Client, dstFile *os.File, mfst ocispec.Descriptor) bool {
	dstFile.Seek(0, 0)
	tr := tar.NewReader(dstFile)

	var blobs []string
	for {
		h, err := tr.Next()
		if err != nil {
			if err == io.EOF {
				break
			}
			t.Fatal(err)
		}

		digest := strings.TrimPrefix(h.Name, "blobs/sha256/")
		if digest != h.Name && digest != "" {
			blobs = append(blobs, digest)
		}
	}

	allPresent := true
	// Check if the archive contains all blobs referenced by the manifest.
	images.Walk(ctx, images.HandlerFunc(func(ctx context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
		if slices.Contains(blobs, desc.Digest.Hex()) {
			return images.Children(ctx, client.ContentStore(), desc)
		}
		allPresent = false
		return nil, images.ErrStopHandler
	}), mfst)

	return allPresent
}

func getPlatformManifest(ctx context.Context, cs content.Store, target ocispec.Descriptor, platform platforms.MatchComparer) (ocispec.Descriptor, error) {
	mfst, err := images.LimitManifests(images.HandlerFunc(func(ctx context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
		children, err := images.Children(ctx, cs, desc)
		if !images.IsManifestType(desc.MediaType) {
			return children, err
		}

		if err != nil {
			if errdefs.IsNotFound(err) {
				return nil, images.ErrSkipDesc
			}
			return nil, err
		}

		return children, nil
	}), platform, 1)(ctx, target)

	if err != nil {
		return ocispec.Descriptor{}, err
	}
	if len(mfst) == 0 {
		return ocispec.Descriptor{}, errdefs.ErrNotFound
	}
	return mfst[0], nil
}

func assertOCITar(t *testing.T, r io.Reader, docker bool) {
	t.Helper()
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
			t.Fatal(err)
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

func assertOCIIndexAnnotationRefName(t *testing.T, r io.Reader) {

	// The required grammar of the well-known org.opencontainer.image.ref.name annotation as specified at
	// https://github.com/opencontainers/image-spec/blob/v1.1.1/annotations.md#pre-defined-annotation-keys
	var ociImageRefNameRegex = regexp.MustCompile(`^[A-Za-z0-9]+(?:(?:[-._:@+]|--)[A-Za-z0-9]+)*(?:/[A-Za-z0-9]+(?:(?:[-._:@+]|--)[A-Za-z0-9]+)*)*$`)

	t.Helper()
	tr := tar.NewReader(r)
	foundIndexJSON := false
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Name == ocispec.ImageIndexFile {
			foundIndexJSON = true
			var idx ocispec.Index
			if err := json.NewDecoder(tr).Decode(&idx); err != nil {
				t.Fatal(err)
			}
			if len(idx.Manifests) == 0 {
				t.Error("index contains no manifests to verify")
			}
			for _, m := range idx.Manifests {
				if ref, ok := m.Annotations[ocispec.AnnotationRefName]; !ok {
					t.Errorf("manifest does not have %s annotation", ocispec.AnnotationRefName)
				} else if !ociImageRefNameRegex.MatchString(ref) {
					t.Errorf("manifest annotation %s=%q does not match required grammar", ocispec.AnnotationRefName, ref)
				}
			}

			break
		}
	}
	if !foundIndexJSON {
		t.Error("index.json not found")
	}
}
