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

func TestStoreResolvesNonDefaultPlatform(t *testing.T) {
	const ref = "containerd.io/multi-platform:latest"
	ctx := context.Background()

	// The image is only present for platform B, which stands in for an image
	// pulled through a runtime handler configured with a foreign platform.
	provider, image, configDigest := newMultiPlatformImage(t, ref, testPlatformB)

	t.Run("a store limited to the platform of the node cannot resolve it", func(t *testing.T) {
		s := NewStore(fakeGetter{image}, provider, platforms.Only(testPlatformA))
		err := s.Update(ctx, ref)
		assertlib.ErrorIs(t, err, errdefs.ErrNotFound)
		assertlib.Empty(t, s.List())
	})

	t.Run("a store that also accepts the configured platform resolves it", func(t *testing.T) {
		s := NewStore(fakeGetter{image}, provider, platforms.Only(testPlatformA), platforms.Only(testPlatformB))
		requirelib.NoError(t, s.Update(ctx, ref))

		id, err := s.Resolve(ref)
		requirelib.NoError(t, err)
		assertlib.Equal(t, configDigest.String(), id)

		got, err := s.Get(id)
		requirelib.NoError(t, err)
		assertlib.Equal(t, testPlatformB.Architecture, got.ImageSpec.Architecture)
		assertlib.Equal(t, []string{ref}, got.References)
		assertlib.Len(t, s.List(), 1)
	})

	t.Run("the platform of the node still wins when both are present", func(t *testing.T) {
		bothProvider, bothImage, _ := newMultiPlatformImage(t, ref, testPlatformA)
		maps.Copy(bothProvider, provider)

		s := NewStore(fakeGetter{bothImage}, bothProvider, platforms.Only(testPlatformA), platforms.Only(testPlatformB))
		requirelib.NoError(t, s.Update(ctx, ref))

		id, err := s.Resolve(ref)
		requirelib.NoError(t, err)
		got, err := s.Get(id)
		requirelib.NoError(t, err)
		assertlib.Equal(t, testPlatformA.Architecture, got.ImageSpec.Architecture)
	})
}

func TestStoreDefaultsToNodePlatform(t *testing.T) {
	s := NewStore(nil, nil)
	requirelib.Len(t, s.matchers, 1)
	assertlib.True(t, s.matchers[0].Match(platforms.DefaultSpec()))
}
