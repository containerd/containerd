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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"testing"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/internal/cri/util"
	"github.com/containerd/errdefs"
	"github.com/containerd/platforms"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	assertlib "github.com/stretchr/testify/assert"
	requirelib "github.com/stretchr/testify/require"
)

// memoryProvider is a content.InfoReaderProvider backed by a map, so that a
// blob can be deliberately left out to represent a platform that was never
// pulled.
type memoryProvider map[digest.Digest][]byte

func (m memoryProvider) ReaderAt(_ context.Context, desc ocispec.Descriptor) (content.ReaderAt, error) {
	b, ok := m[desc.Digest]
	if !ok {
		return nil, fmt.Errorf("content %v: %w", desc.Digest, errdefs.ErrNotFound)
	}
	return memoryReaderAt{Reader: bytes.NewReader(b)}, nil
}

func (m memoryProvider) Info(_ context.Context, dgst digest.Digest) (content.Info, error) {
	b, ok := m[dgst]
	if !ok {
		return content.Info{}, fmt.Errorf("content %v: %w", dgst, errdefs.ErrNotFound)
	}
	return content.Info{Digest: dgst, Size: int64(len(b))}, nil
}

// add marshals v, stores it and returns a descriptor for it.
func (m memoryProvider) add(t *testing.T, mediaType string, v any, platform *ocispec.Platform) ocispec.Descriptor {
	t.Helper()
	b, err := json.Marshal(v)
	requirelib.NoError(t, err)
	dgst := digest.FromBytes(b)
	m[dgst] = b
	return ocispec.Descriptor{
		MediaType: mediaType,
		Digest:    dgst,
		Size:      int64(len(b)),
		Platform:  platform,
	}
}

type memoryReaderAt struct {
	*bytes.Reader
}

func (r memoryReaderAt) Close() error { return nil }

func (r memoryReaderAt) Size() int64 { return r.Reader.Size() }

type fakeGetter struct {
	image images.Image
}

func (g fakeGetter) Get(_ context.Context, name string) (images.Image, error) {
	if name != g.image.Name {
		return images.Image{}, fmt.Errorf("image %q: %w", name, errdefs.ErrNotFound)
	}
	return g.image, nil
}

var (
	testPlatformA = ocispec.Platform{OS: "linux", Architecture: "amd64"}
	testPlatformB = ocispec.Platform{OS: "linux", Architecture: "arm64"}
)

// newMultiPlatformImage builds an image whose index advertises both
// testPlatformA and testPlatformB, but whose content is only present for
// pulledPlatform. This is the shape of a multi-platform image that was pulled
// for a single platform.
func newMultiPlatformImage(t *testing.T, ref string, pulledPlatform ocispec.Platform) (memoryProvider, images.Image, digest.Digest) {
	t.Helper()
	provider := memoryProvider{}

	manifestDescs := make([]ocispec.Descriptor, 0, 2)
	var pulledConfigDigest digest.Digest
	for _, p := range []ocispec.Platform{testPlatformA, testPlatformB} {
		pulled := platforms.Only(pulledPlatform).Match(p)

		layer := []byte("layer-" + p.Architecture)
		layerDigest := digest.FromBytes(layer)
		configBlob := ocispec.Image{
			Platform: p,
			RootFS: ocispec.RootFS{
				Type:    "layers",
				DiffIDs: []digest.Digest{layerDigest},
			},
		}

		// Compute the descriptors for every platform, but only keep the
		// content of the platform that was pulled.
		target := provider
		if !pulled {
			target = memoryProvider{}
		}
		configDesc := target.add(t, ocispec.MediaTypeImageConfig, configBlob, nil)
		if pulled {
			provider[layerDigest] = layer
			pulledConfigDigest = configDesc.Digest
		}
		manifest := ocispec.Manifest{
			MediaType: ocispec.MediaTypeImageManifest,
			Config:    configDesc,
			Layers: []ocispec.Descriptor{{
				MediaType: ocispec.MediaTypeImageLayer,
				Digest:    layerDigest,
				Size:      int64(len(layer)),
			}},
		}
		manifestDescs = append(manifestDescs, target.add(t, ocispec.MediaTypeImageManifest, manifest, &p))
	}

	indexDesc := provider.add(t, ocispec.MediaTypeImageIndex, ocispec.Index{
		MediaType: ocispec.MediaTypeImageIndex,
		Manifests: manifestDescs,
	}, nil)

	return provider, images.Image{Name: ref, Target: indexDesc}, pulledConfigDigest
}

// newSinglePlatformImage builds an image present for exactly one platform.
func newSinglePlatformImage(t *testing.T, ref string, p ocispec.Platform) (memoryProvider, images.Image, digest.Digest) {
	t.Helper()
	provider := memoryProvider{}
	layer := []byte("layer-" + p.Architecture)
	layerDigest := digest.FromBytes(layer)
	provider[layerDigest] = layer
	configDesc := provider.add(t, ocispec.MediaTypeImageConfig, ocispec.Image{
		Platform: p,
		RootFS:   ocispec.RootFS{Type: "layers", DiffIDs: []digest.Digest{layerDigest}},
	}, nil)
	manifestDesc := provider.add(t, ocispec.MediaTypeImageManifest, ocispec.Manifest{
		MediaType: ocispec.MediaTypeImageManifest,
		Config:    configDesc,
		Layers:    []ocispec.Descriptor{{MediaType: ocispec.MediaTypeImageLayer, Digest: layerDigest, Size: int64(len(layer))}},
	}, &p)
	indexDesc := provider.add(t, ocispec.MediaTypeImageIndex, ocispec.Index{
		MediaType: ocispec.MediaTypeImageIndex,
		Manifests: []ocispec.Descriptor{manifestDesc},
	}, nil)
	return provider, images.Image{Name: ref, Target: indexDesc}, configDesc.Digest
}

func TestStoreResolvesNonDefaultPlatform(t *testing.T) {
	const ref = "containerd.io/multi-platform:latest"
	ctx := context.Background()

	// The image is only present for platform B, which stands in for an image
	// pulled through a runtime handler configured with a foreign platform.
	provider, image, configDigest := newMultiPlatformImage(t, ref, testPlatformB)
	s := NewStore(fakeGetter{image}, provider)

	t.Run("it does not resolve on a platform it was not pulled for", func(t *testing.T) {
		requirelib.NoError(t, s.Update(ctx, ref, testPlatformA))
		_, err := s.Resolve(ref, testPlatformA)
		assertlib.ErrorIs(t, err, errdefs.ErrNotFound)
		assertlib.Empty(t, s.List())
	})

	t.Run("it resolves on the platform it was pulled for", func(t *testing.T) {
		requirelib.NoError(t, s.Update(ctx, ref, testPlatformB))

		id, err := s.Resolve(ref, testPlatformB)
		requirelib.NoError(t, err)
		assertlib.Equal(t, configDigest.String(), id)

		got, err := s.Get(id)
		requirelib.NoError(t, err)
		assertlib.Equal(t, testPlatformB.Architecture, got.ImageSpec.Architecture)
		assertlib.Equal(t, testPlatformB.Architecture, got.Platform.Architecture)
		assertlib.Equal(t, []string{ref}, got.References)
		assertlib.Len(t, s.List(), 1)
	})
}

// TestStoreKeepsBothPlatformsOfOneRef is the case the ordered matcher could not
// represent: one reference pulled for two platforms. Each platform has to keep
// its own entry, and the tag has to resolve to the right one on each.
func TestStoreKeepsBothPlatformsOfOneRef(t *testing.T) {
	const tag = "containerd.io/multi-platform:latest"
	ctx := context.Background()

	providerA, image, configA := newMultiPlatformImage(t, tag, testPlatformA)
	providerB, _, configB := newMultiPlatformImage(t, tag, testPlatformB)
	maps.Copy(providerA, providerB)
	requirelib.NotEqual(t, configA, configB)

	s := NewStore(fakeGetter{image}, providerA)
	requirelib.NoError(t, s.Update(ctx, tag, testPlatformA))
	requirelib.NoError(t, s.Update(ctx, tag, testPlatformB))

	for _, tc := range []struct {
		platform ocispec.Platform
		want     digest.Digest
	}{{testPlatformA, configA}, {testPlatformB, configB}} {
		id, err := s.Resolve(tag, tc.platform)
		requirelib.NoError(t, err, "tag must resolve on %v", tc.platform)
		assertlib.Equal(t, tc.want.String(), id)

		got, err := s.Get(id)
		requirelib.NoError(t, err)
		assertlib.Equal(t, tc.platform.Architecture, got.Platform.Architecture)
	}

	// Both platforms are listed, each under its own image id.
	assertlib.Len(t, s.List(), 2)
}

// TestStoreRemovesOnlyTheGivenPlatform pins that dropping a reference on one
// platform leaves the other platform resolvable.
func TestStoreRemovesOnlyTheGivenPlatform(t *testing.T) {
	const tag = "containerd.io/multi-platform:latest"
	ctx := context.Background()

	providerA, image, configA := newMultiPlatformImage(t, tag, testPlatformA)
	providerB, _, configB := newMultiPlatformImage(t, tag, testPlatformB)
	maps.Copy(providerA, providerB)

	s := NewStore(fakeGetter{image}, providerA)
	requirelib.NoError(t, s.Update(ctx, tag, testPlatformA))
	requirelib.NoError(t, s.Update(ctx, tag, testPlatformB))

	// The reference disappears from containerd for platform A only.
	requirelib.NoError(t, s.update(refKey{ref: tag, platform: util.PlatformKey(testPlatformA)}, nil))

	_, err := s.Resolve(tag, testPlatformA)
	assertlib.ErrorIs(t, err, errdefs.ErrNotFound)
	_, err = s.Get(configA.String())
	assertlib.ErrorIs(t, err, errdefs.ErrNotFound)

	id, err := s.Resolve(tag, testPlatformB)
	requirelib.NoError(t, err)
	assertlib.Equal(t, configB.String(), id)
	assertlib.Len(t, s.List(), 1)
}

// TestStoreUnsetPlatformIsTheNode pins that an unset platform and the platform
// of the node are the same key, so callers that do not care keep working.
func TestStoreUnsetPlatformIsTheNode(t *testing.T) {
	const ref = "containerd.io/node-platform:latest"
	ctx := context.Background()

	provider, image, cfg := newSinglePlatformImage(t, ref, platforms.DefaultSpec())
	s := NewStore(fakeGetter{image}, provider)
	requirelib.NoError(t, s.Update(ctx, ref, ocispec.Platform{}))

	id, err := s.Resolve(ref, platforms.DefaultSpec())
	requirelib.NoError(t, err)
	assertlib.Equal(t, cfg.String(), id)
}

// TestStoreDigestRefStaysOnItsOwnPlatform pins that an image id, which is the
// digest of the image config of one platform, is never recorded against
// another. Doing so would add the digest to the references of the other
// platform's image and make the id name the wrong image.
func TestStoreDigestRefStaysOnItsOwnPlatform(t *testing.T) {
	const tag = "containerd.io/multi-platform:latest"
	ctx := context.Background()

	providerA, imageA, configA := newMultiPlatformImage(t, tag, testPlatformA)
	providerB, _, configB := newMultiPlatformImage(t, tag, testPlatformB)
	maps.Copy(providerA, providerB)
	requirelib.NotEqual(t, configA, configB)

	// The image id of platform A, named as a reference, as PullImage records it.
	idA := configA.String()
	getter := multiGetter{images: map[string]images.Image{
		tag: imageA,
		idA: {Name: idA, Target: imageA.Target},
	}}
	s := NewStore(getter, providerA)

	// Refreshing the id on every platform, as the reload path does.
	requirelib.NoError(t, s.Update(ctx, idA, testPlatformA))
	requirelib.NoError(t, s.Update(ctx, idA, testPlatformB))

	// It resolves on its own platform only.
	got, err := s.Resolve(idA, testPlatformA)
	requirelib.NoError(t, err)
	assertlib.Equal(t, idA, got)
	_, err = s.Resolve(idA, testPlatformB)
	assertlib.ErrorIs(t, err, errdefs.ErrNotFound)

	// And it never becomes a reference of the other platform's image.
	requirelib.NoError(t, s.Update(ctx, tag, testPlatformB))
	imgB, err := s.Get(configB.String())
	requirelib.NoError(t, err)
	assertlib.NotContains(t, imgB.References, idA,
		"the image id of another platform must not reference this image")
}

type multiGetter struct {
	images map[string]images.Image
}

func (g multiGetter) Get(_ context.Context, name string) (images.Image, error) {
	i, ok := g.images[name]
	if !ok {
		return images.Image{}, fmt.Errorf("image %q: %w", name, errdefs.ErrNotFound)
	}
	return i, nil
}
