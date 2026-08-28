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

package image

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/images/imagetest"
	"github.com/containerd/errdefs"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestStore(t *testing.T) {
	for _, testCase := range []struct {
		Name       string
		ImageStore *Store
		// Annotations are the different references annotations to run the test with,
		// the possible values:
		// - "OCI": Uses the OCI defined annotation "org.opencontainers.image.ref.name"
		//   This annotation may be a full reference or tag only
		// - "containerd": Uses the containerd defined annotation "io.containerd.image.name"
		//   This annotation is always a full reference as used by containerd
		// - "Annotation": Sets the annotation flag but does not set a reference annotation
		//   Use this case to test the default where no reference is provided
		// - "NoAnnotation": Does not set the annotation flag
		//   Use this case to test storing of the index images by reference
		Annotations []string
		ImageName   string
		Images      []string
		Err         error
	}{
		{
			Name: "Prefix",
			ImageStore: &Store{
				extraReferences: []Reference{
					{
						Name:     "registry.test/image",
						IsPrefix: true,
					},
				},
			},
			Annotations: []string{"OCI", "containerd"},
			ImageName:   "registry.test/image:latest",
			Images:      []string{"registry.test/image:latest"},
		},
		{
			Name: "Overwrite",
			ImageStore: &Store{
				extraReferences: []Reference{
					{
						Name:           "placeholder",
						IsPrefix:       true,
						AllowOverwrite: true,
					},
				},
			},
			Annotations: []string{"OCI", "containerd"},
			ImageName:   "registry.test/image:latest",
			Images:      []string{"registry.test/image:latest"},
		},
		{
			Name: "TagOnly",
			ImageStore: &Store{
				extraReferences: []Reference{
					{
						Name:     "registry.test/image",
						IsPrefix: true,
					},
				},
			},
			Annotations: []string{"OCI"},
			ImageName:   "latest",
			Images:      []string{"registry.test/image:latest"},
		},
		{
			Name: "AddDigest",
			ImageStore: &Store{
				extraReferences: []Reference{
					{
						Name:      "registry.test/base",
						IsPrefix:  true,
						AddDigest: true,
					},
				},
			},
			Annotations: []string{"Annotation"},
			Images:      []string{"registry.test/base@"},
		},
		{
			Name: "NameAndDigest",
			ImageStore: &Store{
				extraReferences: []Reference{
					{
						Name:      "registry.test/base",
						IsPrefix:  true,
						AddDigest: true,
					},
				},
			},
			Annotations: []string{"OCI"},
			ImageName:   "latest",
			Images:      []string{"registry.test/base:latest", "registry.test/base@"},
		},
		{
			Name: "NameSkipDigest",
			ImageStore: &Store{
				extraReferences: []Reference{
					{
						Name:            "registry.test/base",
						IsPrefix:        true,
						AddDigest:       true,
						SkipNamedDigest: true,
					},
				},
			},
			Annotations: []string{"OCI"},
			ImageName:   "latest",
			Images:      []string{"registry.test/base:latest"},
		},
		{
			Name: "OverwriteNameDigest",
			ImageStore: &Store{
				extraReferences: []Reference{
					{
						Name:           "base-name",
						IsPrefix:       true,
						AllowOverwrite: true,
						AddDigest:      true,
					},
				},
			},
			Annotations: []string{"OCI", "containerd"},
			ImageName:   "registry.test/base:latest",
			Images:      []string{"registry.test/base:latest", "base-name@"},
		},
		{
			Name: "OverwriteNameSkipDigest",
			ImageStore: &Store{
				extraReferences: []Reference{
					{
						Name:            "base-name",
						IsPrefix:        true,
						AllowOverwrite:  true,
						AddDigest:       true,
						SkipNamedDigest: true,
					},
				},
			},
			Annotations: []string{"OCI", "containerd"},
			ImageName:   "registry.test/base:latest",
			Images:      []string{"registry.test/base:latest"},
		},
		{
			Name: "ReferenceNotFound",
			ImageStore: &Store{
				extraReferences: []Reference{
					{
						Name:     "registry.test/image",
						IsPrefix: true,
					},
				},
			},
			Annotations: []string{"OCI", "containerd"},
			ImageName:   "registry.test/base:latest",
			Err:         errdefs.ErrNotFound,
		},
		{
			Name:        "NoReference",
			ImageStore:  &Store{},
			Annotations: []string{"Annotation", "NoAnnotation"},
			Err:         errdefs.ErrNotFound,
		},
		{
			Name: "ImageName",
			ImageStore: &Store{
				imageName: "registry.test/index:latest",
			},
			Annotations: []string{"NoAnnotation"},
			Images:      []string{"registry.test/index:latest"},
		},
		{
			Name: "ImageNameWithPrefixAddDigest",
			ImageStore: &Store{
				imageName: "registry.test/index:latest",
				extraReferences: []Reference{
					{
						Name:      "registry.test/base",
						IsPrefix:  true,
						AddDigest: true,
					},
				},
			},
			Annotations: []string{"NoAnnotation"},
			Images:      []string{"registry.test/index:latest", "registry.test/base@"},
		},
		{
			Name: "ImageNameWithExtraRef",
			ImageStore: &Store{
				imageName: "registry.test/index:latest",
				extraReferences: []Reference{
					{
						Name: "registry.test/extra:v1",
					},
				},
			},
			Annotations: []string{"NoAnnotation"},
			Images:      []string{"registry.test/index:latest", "registry.test/extra:v1"},
		},
		{
			// SkipNamedDigest only suppresses digest refs in the annotation
			// branch, where a named reference has been resolved from the
			// descriptor's own annotations via the prefix. In the
			// NoAnnotation branch the top-level imageName is not a
			// prefix-matched name, so the digest ref must still be stored.
			// This matches the integration test DigestRefsSkipNamed where
			// the index digest reference is expected alongside the primary
			// image name.
			Name: "ImageNameWithPrefixAddDigestSkipNamed",
			ImageStore: &Store{
				imageName: "registry.test/index:latest",
				extraReferences: []Reference{
					{
						Name:            "registry.test/base",
						IsPrefix:        true,
						AddDigest:       true,
						SkipNamedDigest: true,
					},
				},
			},
			Annotations: []string{"NoAnnotation"},
			Images:      []string{"registry.test/index:latest", "registry.test/base@"},
		},
	} {
		for _, a := range testCase.Annotations {
			name := testCase.Name + "_" + a
			dgst := digest.Canonical.FromString(name)
			desc := ocispec.Descriptor{
				Digest:      dgst,
				Annotations: map[string]string{},
			}
			expected := make([]string, len(testCase.Images))
			for i, img := range testCase.Images {
				if img[len(img)-1] == '@' {
					img = img + dgst.String()
				}
				expected[i] = img
			}
			switch a {
			case "containerd":
				desc.Annotations["io.containerd.import.ref-source"] = "annotation"
				desc.Annotations[images.AnnotationImageName] = testCase.ImageName
			case "OCI":
				desc.Annotations["io.containerd.import.ref-source"] = "annotation"
				desc.Annotations[ocispec.AnnotationRefName] = testCase.ImageName
			case "Annotation":
				desc.Annotations["io.containerd.import.ref-source"] = "annotation"
			}
			t.Run(name, func(t *testing.T) {
				imgs, err := testCase.ImageStore.Store(context.Background(), desc, newSimpleImageStore())
				if err != nil {
					if testCase.Err == nil {
						t.Fatal(err)
					}
					if !errors.Is(err, testCase.Err) {
						t.Fatalf("unexpected error %v: expected %v", err, testCase.Err)
					}
					return
				} else if testCase.Err != nil {
					t.Fatalf("succeeded but expected error: %v", testCase.Err)
				}

				if len(imgs) != len(expected) {
					t.Fatalf("mismatched array length\nexpected:\n\t%v\nactual\n\t%v", expected, imgs)
				}
				primaryName := testCase.ImageStore.imageName
				for i, name := range expected {
					if imgs[i].Name != name {
						t.Fatalf("wrong image name %q, expected %q", imgs[i].Name, name)
					}
					if imgs[i].Target.Digest != dgst {
						t.Fatalf("wrong image digest %s, expected %s", imgs[i].Target.Digest, dgst)
					}

					// The primary image is never collectible via back-reference;
					// extra references are tied to the primary via gc.bref.image
					// with an immediate gc.expire so they are collected alongside
					// it. When no primary image is set, extra references are
					// standalone and receive no GC labels.
					bref := imgs[i].Labels["containerd.io/gc.bref.image"]
					expire := imgs[i].Labels["containerd.io/gc.expire"]
					switch {
					case imgs[i].Name == primaryName:
						if bref != "" || expire != "" {
							t.Fatalf("primary image %q unexpectedly has GC labels: bref=%q expire=%q", imgs[i].Name, bref, expire)
						}
					case primaryName == "":
						if bref != "" || expire != "" {
							t.Fatalf("extra ref %q unexpectedly has GC labels when no primary image: bref=%q expire=%q", imgs[i].Name, bref, expire)
						}
					default:
						if bref != primaryName {
							t.Fatalf("extra ref %q has gc.bref.image=%q, expected %q", imgs[i].Name, bref, primaryName)
						}
						if expire == "" {
							t.Fatalf("extra ref %q missing gc.expire label", imgs[i].Name)
						}
						if _, err := time.Parse(time.RFC3339, expire); err != nil {
							t.Fatalf("extra ref %q has invalid gc.expire %q: %v", imgs[i].Name, expire, err)
						}
					}
				}
			})
		}

	}
}

func TestLookup(t *testing.T) {
	ctx := context.Background()
	is := newSimpleImageStore()
	for _, name := range []string{
		"registry.io/test1:latest",
		"registry.io/test1:v1",
	} {
		is.Create(ctx, images.Image{
			Name: name,
		})
	}
	for _, testCase := range []struct {
		Name       string
		ImageStore *Store
		Expected   []string
		Err        error
	}{
		{
			Name: "SingleImage",
			ImageStore: &Store{
				imageName: "registry.io/test1:latest",
			},
			Expected: []string{"registry.io/test1:latest"},
		},
		{
			Name: "MultipleReferences",
			ImageStore: &Store{
				imageName: "registry.io/test1:latest",
				extraReferences: []Reference{
					{
						Name: "registry.io/test1:v1",
					},
				},
			},
			Expected: []string{"registry.io/test1:latest", "registry.io/test1:v1"},
		},
		{
			Name: "OnlyReferences",
			ImageStore: &Store{
				extraReferences: []Reference{
					{
						Name: "registry.io/test1:latest",
					},
					{
						Name: "registry.io/test1:v1",
					},
				},
			},
			Expected: []string{"registry.io/test1:latest", "registry.io/test1:v1"},
		},
		{
			Name: "UnsupportedPrefix",
			ImageStore: &Store{
				extraReferences: []Reference{
					{
						Name:     "registry.io/test1:latest",
						IsPrefix: true,
					},
				},
			},
			Err: errdefs.ErrNotImplemented,
		},
	} {
		t.Run(testCase.Name, func(t *testing.T) {
			images, err := testCase.ImageStore.Lookup(ctx, is)
			if err != nil {
				if !errors.Is(err, testCase.Err) {
					t.Errorf("unexpected error %v, expected %v", err, testCase.Err)
				}
				return
			} else if testCase.Err != nil {
				t.Fatal("expected error")
			}
			imageNames := make([]string, len(images))
			for i, img := range images {
				imageNames[i] = img.Name
			}
			sort.Strings(imageNames)
			sort.Strings(testCase.Expected)
			if len(images) != len(testCase.Expected) {
				t.Fatalf("unexpected images:\n\t%v\nexpected:\n\t%v", imageNames, testCase.Expected)
			}
			for i := range imageNames {
				if imageNames[i] != testCase.Expected[i] {
					t.Fatalf("unexpected images:\n\t%v\nexpected:\n\t%v", imageNames, testCase.Expected)
				}
			}
		})
	}
}

// simpleImageStore is for testing images in memory,
// no filter support
type simpleImageStore struct {
	l      sync.Mutex
	images map[string]images.Image
}

func newSimpleImageStore() images.Store {
	return &simpleImageStore{
		images: make(map[string]images.Image),
	}
}

func (is *simpleImageStore) Get(ctx context.Context, name string) (images.Image, error) {
	is.l.Lock()
	defer is.l.Unlock()
	img, ok := is.images[name]
	if !ok {
		return images.Image{}, errdefs.ErrNotFound
	}
	return img, nil
}

func (is *simpleImageStore) List(ctx context.Context, filters ...string) ([]images.Image, error) {
	is.l.Lock()
	defer is.l.Unlock()
	var imgs []images.Image

	// filters not supported, return all
	for _, img := range is.images {
		imgs = append(imgs, img)
	}
	return imgs, nil
}

func (is *simpleImageStore) Create(ctx context.Context, image images.Image) (images.Image, error) {
	is.l.Lock()
	defer is.l.Unlock()

	if _, ok := is.images[image.Name]; ok {
		return images.Image{}, errdefs.ErrAlreadyExists
	}
	is.images[image.Name] = image

	return image, nil
}

func (is *simpleImageStore) Update(ctx context.Context, image images.Image, fieldpaths ...string) (images.Image, error) {
	is.l.Lock()
	defer is.l.Unlock()

	if _, ok := is.images[image.Name]; !ok {
		return images.Image{}, errdefs.ErrNotFound
	}
	// fieldpaths no supported, update entire image
	is.images[image.Name] = image

	return image, nil
}

func (is *simpleImageStore) Delete(ctx context.Context, name string, opts ...images.DeleteOpt) error {
	is.l.Lock()
	defer is.l.Unlock()

	if _, ok := is.images[name]; !ok {
		return errdefs.ErrNotFound
	}
	delete(is.images, name)

	return nil
}

// visitedDigests dispatches h over root and returns the digest of every
// descriptor h.Handle was called for.
func visitedDigests(t *testing.T, ctx context.Context, h images.HandlerFunc, root ocispec.Descriptor) []digest.Digest {
	t.Helper()

	var (
		mu      sync.Mutex
		visited []digest.Digest
	)
	record := images.HandlerFunc(func(_ context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
		mu.Lock()
		visited = append(visited, desc.Digest)
		mu.Unlock()
		return nil, nil
	})
	if err := images.Dispatch(ctx, images.Handlers(record, h), nil, root); err != nil {
		t.Fatal(err)
	}
	return visited
}

func containsDigest(digests []digest.Digest, d digest.Digest) bool {
	for _, v := range digests {
		if v == d {
			return true
		}
	}
	return false
}

// plainAndErofsManifests builds two distinguishable (different digest
// throughout) manifests for the same linux/amd64 platform: one plain, and
// one additionally requiring OSFeatures ["erofs"].
func plainAndErofsManifests(tc imagetest.ContentStore) (plain, erofs imagetest.Content) {
	plain = imagetest.AddPlatform(tc.Manifest(
		tc.JSONObject(ocispec.MediaTypeImageConfig, ocispec.ImageConfig{Env: []string{"plain"}}),
		tc.Blob(ocispec.MediaTypeImageLayerGzip, []byte("plain-layer-content")),
	), ocispec.Platform{OS: "linux", Architecture: "amd64"})
	erofs = imagetest.AddPlatform(tc.Manifest(
		tc.JSONObject(ocispec.MediaTypeImageConfig, ocispec.ImageConfig{Env: []string{"erofs"}}),
		tc.Blob(ocispec.MediaTypeImageLayerGzip, []byte("erofs-layer-content")),
	), ocispec.Platform{OS: "linux", Architecture: "amd64", OSFeatures: []string{"erofs"}})
	return plain, erofs
}

var (
	amd64Spec      = ocispec.Platform{OS: "linux", Architecture: "amd64"}
	amd64ErofsSpec = ocispec.Platform{OS: "linux", Architecture: "amd64", OSFeatures: []string{"erofs"}}
	arm64Spec      = ocispec.Platform{OS: "linux", Architecture: "arm64"}
)

// TestImageFilterWithPlatformsPrefersRefinedOSFeatureMatch verifies that,
// given a refinement describing the same platform as a configured one
// (see WithPlatforms) but with OSFeatures ["erofs"] (e.g. the platform of
// a matched unpack configuration), an index containing both a plain
// manifest and one requiring OSFeatures ["erofs"] for that platform has
// its layer fetched for the erofs manifest only, while - in
// WithAllMetadata mode - both manifests and their configs remain
// reachable.
func TestImageFilterWithPlatformsPrefersRefinedOSFeatureMatch(t *testing.T) {
	ctx := context.Background()
	tc := imagetest.NewContentStore(ctx, t)

	plain, erofs := plainAndErofsManifests(tc)
	idx := tc.Index(plain, erofs)

	is := NewStore("", WithAllMetadata, WithPlatforms(amd64Spec))
	h := is.ImageFilterWithPlatforms(images.ChildrenHandler(tc.Store), tc.Store, []ocispec.Platform{amd64ErofsSpec})

	visited := visitedDigests(t, ctx, h, idx.Descriptor)

	for _, want := range []ocispec.Descriptor{
		idx.Descriptor, plain.Descriptor, plain.Children[0].Descriptor, erofs.Descriptor, erofs.Children[0].Descriptor, erofs.Children[1].Descriptor,
	} {
		if !containsDigest(visited, want.Digest) {
			t.Errorf("expected %s (%s) to be visited", want.Digest, want.MediaType)
		}
	}
	if plainLayer := plain.Children[1].Descriptor; containsDigest(visited, plainLayer.Digest) {
		t.Errorf("expected the non-preferred plain manifest's layer %s not to be fetched", plainLayer.Digest)
	}
}

// TestImageFilterWithPlatformsDefaultKeepsSingleBestVariant verifies that,
// outside of WithAllMetadata mode, only the single best variant of a
// configured platform - and none of its non-preferred siblings - remains
// reachable at all, preserving the existing minimal (non-metadata) fetch
// behavior.
func TestImageFilterWithPlatformsDefaultKeepsSingleBestVariant(t *testing.T) {
	ctx := context.Background()
	tc := imagetest.NewContentStore(ctx, t)

	plain, erofs := plainAndErofsManifests(tc)
	idx := tc.Index(plain, erofs)

	is := NewStore("", WithPlatforms(amd64Spec))
	h := is.ImageFilterWithPlatforms(images.ChildrenHandler(tc.Store), tc.Store, []ocispec.Platform{amd64ErofsSpec})

	visited := visitedDigests(t, ctx, h, idx.Descriptor)

	if !containsDigest(visited, erofs.Descriptor.Digest) || !containsDigest(visited, erofs.Children[1].Descriptor.Digest) {
		t.Errorf("expected the preferred erofs manifest and its layer to be visited, got %v", visited)
	}
	if containsDigest(visited, plain.Descriptor.Digest) {
		t.Errorf("expected the non-preferred plain manifest not to be visited at all, got %v", visited)
	}
}

// TestImageFilterWithPlatformsAllPlatformsKeepsEveryVariant is a
// regression test: with no configured platforms (WithPlatforms not used,
// i.e. `ctr pull --all-platforms`), every manifest's layers must be
// fetched, including every variant of a platform sharing OSFeatures with
// another - not just a single "global best" - since nothing was actually
// requested to narrow selection to one.
func TestImageFilterWithPlatformsAllPlatformsKeepsEveryVariant(t *testing.T) {
	ctx := context.Background()
	tc := imagetest.NewContentStore(ctx, t)

	plain, erofs := plainAndErofsManifests(tc)
	idx := tc.Index(plain, erofs)

	is := &Store{}
	h := is.ImageFilterWithPlatforms(images.ChildrenHandler(tc.Store), tc.Store, nil)

	visited := visitedDigests(t, ctx, h, idx.Descriptor)

	for _, want := range []ocispec.Descriptor{
		plain.Children[1].Descriptor, erofs.Children[1].Descriptor,
	} {
		if !containsDigest(visited, want.Digest) {
			t.Errorf("expected layer %s to be fetched with no configured platforms", want.Digest)
		}
	}
}

// TestImageFilterWithPlatformsMultiplePlatformsKeepsEachPlatform is a
// regression test: with more than one configured platform (`ctr pull
// --platform linux/amd64 --platform linux/arm64`), each distinct
// platform's manifest must keep its layers - the best-variant selection
// must not collapse to a single manifest across unrelated platforms.
func TestImageFilterWithPlatformsMultiplePlatformsKeepsEachPlatform(t *testing.T) {
	ctx := context.Background()
	tc := imagetest.NewContentStore(ctx, t)

	amd64 := imagetest.AddPlatform(tc.Manifest(
		tc.JSONObject(ocispec.MediaTypeImageConfig, ocispec.ImageConfig{Env: []string{"amd64"}}),
		tc.Blob(ocispec.MediaTypeImageLayerGzip, []byte("amd64-layer-content")),
	), amd64Spec)
	arm64 := imagetest.AddPlatform(tc.Manifest(
		tc.JSONObject(ocispec.MediaTypeImageConfig, ocispec.ImageConfig{Env: []string{"arm64"}}),
		tc.Blob(ocispec.MediaTypeImageLayerGzip, []byte("arm64-layer-content")),
	), arm64Spec)
	idx := tc.Index(amd64, arm64)

	is := NewStore("", WithPlatforms(amd64Spec, arm64Spec))
	h := is.ImageFilterWithPlatforms(images.ChildrenHandler(tc.Store), tc.Store, nil)

	visited := visitedDigests(t, ctx, h, idx.Descriptor)

	for _, want := range []ocispec.Descriptor{
		amd64.Children[1].Descriptor, arm64.Children[1].Descriptor,
	} {
		if !containsDigest(visited, want.Digest) {
			t.Errorf("expected layer %s to be fetched, got %v", want.Digest, visited)
		}
	}
}

// TestImageFilterWithPlatformsMultiplePlatformsWithVariantDoesNotCrowdOut
// is a regression test for a specific failure mode a single combined sort
// across every requested platform is prone to: with amd64 requesting an
// erofs refinement (so both a plain and an erofs amd64 manifest are
// acceptable substitutes) alongside a plain arm64 request, the two amd64
// candidates must not both outrank - and so crowd out - arm64's sole
// candidate. Each configured platform is selected independently instead
// (see images.SelectManifestsPerPlatform), so amd64 keeps only its best
// (erofs) variant and arm64 keeps its own manifest regardless.
func TestImageFilterWithPlatformsMultiplePlatformsWithVariantDoesNotCrowdOut(t *testing.T) {
	ctx := context.Background()
	tc := imagetest.NewContentStore(ctx, t)

	plain, erofs := plainAndErofsManifests(tc)
	arm64 := imagetest.AddPlatform(tc.Manifest(
		tc.JSONObject(ocispec.MediaTypeImageConfig, ocispec.ImageConfig{Env: []string{"arm64"}}),
		tc.Blob(ocispec.MediaTypeImageLayerGzip, []byte("arm64-layer-content")),
	), arm64Spec)
	idx := tc.Index(plain, erofs, arm64)

	is := NewStore("", WithPlatforms(amd64Spec, arm64Spec))
	h := is.ImageFilterWithPlatforms(images.ChildrenHandler(tc.Store), tc.Store, []ocispec.Platform{amd64ErofsSpec})

	visited := visitedDigests(t, ctx, h, idx.Descriptor)

	if !containsDigest(visited, erofs.Children[1].Descriptor.Digest) {
		t.Errorf("expected amd64's erofs variant to keep its layers, got %v", visited)
	}
	if !containsDigest(visited, arm64.Children[1].Descriptor.Digest) {
		t.Errorf("expected arm64 to keep its layers even though amd64 had two candidates, got %v", visited)
	}
	if containsDigest(visited, plain.Descriptor.Digest) {
		t.Errorf("expected the non-preferred amd64 plain manifest not to be visited at all, got %v", visited)
	}
}

func TestRefinePlatforms(t *testing.T) {
	t.Run("no refinements returns configured unchanged", func(t *testing.T) {
		configured := []ocispec.Platform{amd64Spec}
		got := refinePlatforms(configured, nil)
		if len(got) != 1 || got[0].OS != "linux" || got[0].Architecture != "amd64" || len(got[0].OSFeatures) != 0 {
			t.Fatalf("expected configured unchanged, got %v", got)
		}
	})

	t.Run("adopts OSFeatures from a same-platform refinement", func(t *testing.T) {
		got := refinePlatforms([]ocispec.Platform{amd64Spec}, []ocispec.Platform{amd64ErofsSpec})
		if len(got) != 1 {
			t.Fatalf("expected 1 platform, got %d", len(got))
		}
		if len(got[0].OSFeatures) != 1 || got[0].OSFeatures[0] != "erofs" {
			t.Fatalf("expected OSFeatures [erofs], got %v", got[0].OSFeatures)
		}
	})

	t.Run("ignores a refinement for a different architecture", func(t *testing.T) {
		arm64ErofsSpec := ocispec.Platform{OS: "linux", Architecture: "arm64", OSFeatures: []string{"erofs"}}
		got := refinePlatforms([]ocispec.Platform{amd64Spec}, []ocispec.Platform{arm64ErofsSpec})
		if len(got[0].OSFeatures) != 0 {
			t.Fatalf("expected the amd64 entry untouched by an arm64 refinement, got %v", got[0])
		}
	})

	t.Run("ignores a refinement whose OSFeatures are not a superset", func(t *testing.T) {
		configured := []ocispec.Platform{amd64ErofsSpec}
		got := refinePlatforms(configured, []ocispec.Platform{amd64Spec})
		if len(got[0].OSFeatures) != 1 || got[0].OSFeatures[0] != "erofs" {
			t.Fatalf("expected the configured entry's OSFeatures preserved, got %v", got[0])
		}
	})

	t.Run("prefers the richest qualifying refinement", func(t *testing.T) {
		richer := ocispec.Platform{OS: "linux", Architecture: "amd64", OSFeatures: []string{"erofs", "extra"}}
		got := refinePlatforms([]ocispec.Platform{amd64Spec}, []ocispec.Platform{amd64ErofsSpec, richer})
		if len(got[0].OSFeatures) != 2 {
			t.Fatalf("expected the richer refinement to win, got %v", got[0])
		}
	})

	t.Run("only refines the matching entry among several configured platforms", func(t *testing.T) {
		got := refinePlatforms([]ocispec.Platform{amd64Spec, arm64Spec}, []ocispec.Platform{amd64ErofsSpec})
		if len(got[0].OSFeatures) != 1 {
			t.Fatalf("expected amd64 refined, got %v", got[0])
		}
		if len(got[1].OSFeatures) != 0 {
			t.Fatalf("expected arm64 untouched, got %v", got[1])
		}
	})
}

func TestIsSupersetOSFeatures(t *testing.T) {
	for _, tc := range []struct {
		name             string
		superset, subset []string
		want             bool
	}{
		{"empty subset always satisfied", []string{}, []string{}, true},
		{"empty subset satisfied by non-empty superset", []string{"erofs"}, []string{}, true},
		{"identical", []string{"erofs"}, []string{"erofs"}, true},
		{"proper superset", []string{"a", "erofs"}, []string{"erofs"}, true},
		{"missing feature", []string{"a"}, []string{"erofs"}, false},
		{"superset shorter than subset", []string{"erofs"}, []string{"a", "erofs"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isSupersetOSFeatures(tc.superset, tc.subset); got != tc.want {
				t.Errorf("isSupersetOSFeatures(%v, %v) = %v, want %v", tc.superset, tc.subset, got, tc.want)
			}
		})
	}
}
