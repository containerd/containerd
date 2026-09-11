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

package unpack

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/containerd/containerd/v2/core/diff"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/images/imagetest"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/core/remotes"
	"github.com/containerd/containerd/v2/core/remotes/docker"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/pkg/testutil"
	"github.com/containerd/errdefs"
	"github.com/containerd/platforms"
)

// failApplier fails the test if Apply is called.
type failApplier struct{ t *testing.T }

func (a failApplier) Apply(_ context.Context, desc ocispec.Descriptor, _ []mount.Mount, _ ...diff.ApplyOpt) (ocispec.Descriptor, error) {
	a.t.Errorf("Apply must not be called for layer %s", desc.Digest)
	return ocispec.Descriptor{}, nil
}

func layerDesc(id string) ocispec.Descriptor {
	return ocispec.Descriptor{MediaType: ocispec.MediaTypeImageLayerGzip, Digest: digest.FromString(id), Size: 1}
}

func manifestDesc(id string) ocispec.Descriptor {
	return ocispec.Descriptor{MediaType: ocispec.MediaTypeImageManifest, Digest: digest.FromString(id), Size: 1}
}

// alreadyExistsSnapshotter answers every Prepare with AlreadyExists and finds
// the chain in Stat, so unpack skips each layer without fetching it.
type alreadyExistsSnapshotter struct{ snapshots.Snapshotter }

func (alreadyExistsSnapshotter) Prepare(context.Context, string, string, ...snapshots.Opt) ([]mount.Mount, error) {
	return nil, errdefs.ErrAlreadyExists
}

func (alreadyExistsSnapshotter) Stat(_ context.Context, key string) (snapshots.Info, error) {
	return snapshots.Info{Name: key}, nil
}

// TestUnpackConfigSharingManifests verifies that when several manifests share a
// config, every manifest's layers are fetched even when the snapshots exist.
// Only one unpack is scheduled per config. A lone manifest skips fetching
// layers whose snapshots exist, and a manifest without layers is ignored.
func TestUnpackConfigSharingManifests(t *testing.T) {
	ctx := context.Background()
	cs := imagetest.NewContentStore(ctx, t)

	config := cs.JSONObject(ocispec.MediaTypeImageConfig, ocispec.Image{
		Platform: ocispec.Platform{OS: "linux", Architecture: "amd64"},
		RootFS:   ocispec.RootFS{Type: "layers", DiffIDs: []digest.Digest{digest.FromString("diff-0"), digest.FromString("diff-1")}},
	}).Descriptor
	manifestA, manifestB := manifestDesc("manifest-a"), manifestDesc("manifest-b")
	layersA := []ocispec.Descriptor{layerDesc("a-0"), layerDesc("a-1")}
	layersB := []ocispec.Descriptor{layerDesc("b-0"), layerDesc("b-1")}
	sharedConfig := map[digest.Digest][]ocispec.Descriptor{
		manifestA.Digest: append([]ocispec.Descriptor{config}, layersA...),
		manifestB.Digest: append([]ocispec.Descriptor{config}, layersB...),
	}

	for _, tc := range []struct {
		name        string
		children    map[digest.Digest][]ocispec.Descriptor
		order       []ocispec.Descriptor
		wantFetched []ocispec.Descriptor
		wantUnpacks int
	}{
		{
			name:        "config after both manifests",
			children:    sharedConfig,
			order:       []ocispec.Descriptor{manifestA, manifestB, config, config},
			wantFetched: slices.Concat([]ocispec.Descriptor{manifestA, manifestB, config}, layersA, layersB),
			wantUnpacks: 1,
		},
		{
			name:        "config between the manifests",
			children:    sharedConfig,
			order:       []ocispec.Descriptor{manifestA, config, manifestB, config},
			wantFetched: slices.Concat([]ocispec.Descriptor{manifestA, manifestB, config}, layersA, layersB),
			wantUnpacks: 1,
		},
		{
			name:        "lone manifest with existing snapshots",
			children:    sharedConfig,
			order:       []ocispec.Descriptor{manifestA, config},
			wantFetched: []ocispec.Descriptor{manifestA, config},
			wantUnpacks: 1,
		},
		{
			name:        "manifest without layers",
			children:    map[digest.Digest][]ocispec.Descriptor{manifestA.Digest: {config}},
			order:       []ocispec.Descriptor{manifestA, config},
			wantFetched: []ocispec.Descriptor{manifestA, config},
			wantUnpacks: 0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			fetched := map[digest.Digest]struct{}{}
			h := images.HandlerFunc(func(ctx context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
				mu.Lock()
				fetched[desc.Digest] = struct{}{}
				mu.Unlock()
				return tc.children[desc.Digest], nil
			})

			u, err := NewUnpacker(ctx, cs.Store, WithUnpackPlatform(Platform{
				Snapshotter: alreadyExistsSnapshotter{},
				Applier:     failApplier{t},
			}))
			require.NoError(t, err)

			wrapped := u.Unpack(h)
			for _, desc := range tc.order {
				_, err := wrapped.Handle(ctx, desc)
				require.NoError(t, err)
			}
			res, err := u.Wait()
			require.NoError(t, err)

			want := map[digest.Digest]struct{}{}
			for _, desc := range tc.wantFetched {
				want[desc.Digest] = struct{}{}
			}
			assert.Equal(t, want, fetched)
			assert.Equal(t, tc.wantUnpacks, res.Unpacks)
		})
	}
}

// TestPullFetchesLayersOfEveryConfigSharingManifest verifies that a pull
// through the real handler chain, from a local registry, fetches the layers
// of every config-sharing manifest into the content store.
func TestPullFetchesLayersOfEveryConfigSharingManifest(t *testing.T) {
	ctx := context.Background()

	imagePlatform := ocispec.Platform{OS: "linux", Architecture: "amd64"}

	// One config, shared by both manifests. Both can share it because a config
	// records each layer's uncompressed digest (diffID), which compression does
	// not change.
	config := mustMarshal(t, ocispec.Image{
		Platform: imagePlatform,
		RootFS:   ocispec.RootFS{Type: "layers", DiffIDs: []digest.Digest{digest.FromString("diff-0")}},
	})
	configDesc := blobDesc(ocispec.MediaTypeImageConfig, config)

	layerA := []byte("layer-a-compressed-bytes")
	layerB := []byte("layer-b-compressed-bytes")
	layerADesc := blobDesc(ocispec.MediaTypeImageLayerGzip, layerA)
	layerBDesc := blobDesc(ocispec.MediaTypeImageLayerGzip, layerB)

	manifestA := mustMarshal(t, ocispec.Manifest{
		MediaType: ocispec.MediaTypeImageManifest,
		Config:    configDesc,
		Layers:    []ocispec.Descriptor{layerADesc},
	})
	manifestB := mustMarshal(t, ocispec.Manifest{
		MediaType: ocispec.MediaTypeImageManifest,
		Config:    configDesc,
		Layers:    []ocispec.Descriptor{layerBDesc},
	})
	manifestADesc := blobDesc(ocispec.MediaTypeImageManifest, manifestA)
	manifestADesc.Platform = &imagePlatform
	manifestBDesc := blobDesc(ocispec.MediaTypeImageManifest, manifestB)
	manifestBDesc.Platform = &imagePlatform

	index := mustMarshal(t, ocispec.Index{
		MediaType: ocispec.MediaTypeImageIndex,
		Manifests: []ocispec.Descriptor{manifestADesc, manifestBDesc},
	})
	indexDesc := blobDesc(ocispec.MediaTypeImageIndex, index)

	ref := testutil.ServeImage(t, "img", "latest", indexDesc, map[digest.Digest][]byte{
		indexDesc.Digest:     index,
		manifestADesc.Digest: manifestA,
		manifestBDesc.Digest: manifestB,
		configDesc.Digest:    config,
		layerADesc.Digest:    layerA,
		layerBDesc.Digest:    layerB,
	})

	store := imagetest.NewContentStore(ctx, t).Store

	// NewResolver uses plain HTTP for localhost, where the registry listens,
	// so no hosts configuration is needed.
	resolver := docker.NewResolver(docker.ResolverOptions{})
	_, resolved, err := resolver.Resolve(ctx, ref)
	require.NoError(t, err)
	fetcher, err := resolver.Fetcher(ctx, ref)
	require.NoError(t, err)

	// Assemble the same handler chain a transfer-service pull uses.
	children := images.FilterPlatforms(images.ChildrenHandler(store), platforms.Only(imagePlatform))
	handler := images.Handlers(remotes.FetchHandler(store, fetcher), children)

	u, err := NewUnpacker(ctx, store, WithUnpackPlatform(Platform{
		// No image matches, so unpack fetches every layer instead of applying
		// it, and no real snapshotter is needed.
		Platform:    platforms.OnlyStrict(platforms.MustParse("linux/arm64")),
		Snapshotter: alreadyExistsSnapshotter{},
		Applier:     failApplier{t},
	}))
	require.NoError(t, err)

	require.NoError(t, images.Dispatch(ctx, u.Unpack(handler), nil, resolved))
	_, err = u.Wait()
	require.NoError(t, err)

	_, err = store.Info(ctx, layerADesc.Digest)
	require.NoError(t, err, "layer of the first config-sharing manifest should be present, but is not")
	_, err = store.Info(ctx, layerBDesc.Digest)
	require.NoError(t, err, "layer of the second config-sharing manifest should be present, but is not")
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}

func blobDesc(mediaType string, body []byte) ocispec.Descriptor {
	return ocispec.Descriptor{
		MediaType: mediaType,
		Digest:    digest.FromBytes(body),
		Size:      int64(len(body)),
	}
}
