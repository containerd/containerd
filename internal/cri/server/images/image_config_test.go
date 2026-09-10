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

package images

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/errdefs"
	"github.com/containerd/platforms"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// blobStore is a content.Store holding only the blobs it was given, so that a
// platform can be made partially present.
type blobStore struct {
	content.Store
	blobs map[digest.Digest][]byte
}

func (b blobStore) ReaderAt(_ context.Context, desc ocispec.Descriptor) (content.ReaderAt, error) {
	raw, ok := b.blobs[desc.Digest]
	if !ok {
		return nil, fmt.Errorf("content %v: %w", desc.Digest, errdefs.ErrNotFound)
	}
	return blobReaderAt{bytes.NewReader(raw)}, nil
}

func (b blobStore) Info(_ context.Context, dgst digest.Digest) (content.Info, error) {
	raw, ok := b.blobs[dgst]
	if !ok {
		return content.Info{}, fmt.Errorf("content %v: %w", dgst, errdefs.ErrNotFound)
	}
	return content.Info{Digest: dgst, Size: int64(len(raw))}, nil
}

type blobReaderAt struct{ *bytes.Reader }

func (r blobReaderAt) Close() error { return nil }
func (r blobReaderAt) Size() int64  { return r.Reader.Size() }

// fakeImage is a containerd.Image exposing only what imageConfig uses.
type fakeImage struct {
	containerd.Image
	target ocispec.Descriptor
	store  content.Store
}

func (f fakeImage) Target() ocispec.Descriptor  { return f.target }
func (f fakeImage) ContentStore() content.Store { return f.store }

func marshalBlob(t *testing.T, blobs map[digest.Digest][]byte, mediaType string, v any, platform *ocispec.Platform, present bool) ocispec.Descriptor {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	dgst := digest.FromBytes(raw)
	if present {
		blobs[dgst] = raw
	}
	return ocispec.Descriptor{MediaType: mediaType, Digest: dgst, Size: int64(len(raw)), Platform: platform}
}

// TestImageConfigSkipsPlatformWithoutConfigBlob builds an index advertising two
// platforms where the first one has its manifest but not its image config, the
// state a torn pull leaves behind. imageConfig must skip it and pick the
// platform the image store would also resolve, otherwise UpdateImage mints an
// image id reference for a platform the store then rejects.
func TestImageConfigSkipsPlatformWithoutConfigBlob(t *testing.T) {
	nodePlatform := platforms.DefaultSpec()
	otherPlatform := ocispec.Platform{OS: "linux", Architecture: "mips64le"}

	blobs := map[digest.Digest][]byte{}
	var manifests []ocispec.Descriptor
	var wantDigest digest.Digest

	for _, tc := range []struct {
		platform    ocispec.Platform
		configFound bool
	}{
		{nodePlatform, false}, // manifest present, config missing
		{otherPlatform, true}, // fully present
	} {
		p := tc.platform
		layer := []byte("layer-" + p.Architecture)
		layerDigest := digest.FromBytes(layer)
		configDesc := marshalBlob(t, blobs, ocispec.MediaTypeImageConfig, ocispec.Image{
			Platform: p,
			RootFS:   ocispec.RootFS{Type: "layers", DiffIDs: []digest.Digest{layerDigest}},
		}, nil, tc.configFound)
		if tc.configFound {
			wantDigest = configDesc.Digest
		}
		manifests = append(manifests, marshalBlob(t, blobs, ocispec.MediaTypeImageManifest, ocispec.Manifest{
			MediaType: ocispec.MediaTypeImageManifest,
			Config:    configDesc,
			Layers:    []ocispec.Descriptor{{MediaType: ocispec.MediaTypeImageLayer, Digest: layerDigest, Size: int64(len(layer))}},
		}, &p, true))
	}

	index := marshalBlob(t, blobs, ocispec.MediaTypeImageIndex, ocispec.Index{
		MediaType: ocispec.MediaTypeImageIndex,
		Manifests: manifests,
	}, nil, true)

	c, _ := newTestCRIService()
	c.imagePlatforms = imagePlatforms(map[string]ImagePlatform{
		"runc-other": {Platform: otherPlatform},
	})

	desc, err := c.imageConfig(context.Background(), fakeImage{
		target: index,
		store:  blobStore{blobs: blobs},
	})
	require.NoError(t, err)
	assert.Equal(t, wantDigest, desc.Digest, "should select the platform whose config is present locally")
}
