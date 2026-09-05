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
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sync"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/identity"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/semaphore"

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

func generateRandomDiffIDs(t testing.TB, num int) []digest.Digest {
	const size = 10
	diffIDs := make([]digest.Digest, 0, num)
	for range num {
		b := make([]byte, size)
		_, err := rand.Read(b)
		if err != nil {
			t.Fatalf("failed to generate random bytes: %v", err)
		}
		diffIDs = append(diffIDs, digest.FromBytes(b))
	}
	return diffIDs
}

func BenchmarkUnpackWithChainID(b *testing.B) {
	// This simulates the old way of repeatedly calculating per-layer chainID
	// as we unpack every layers, by calling `identity.ChainID`.
	unpackWithChainID := func(diffIDs []digest.Digest) {
		var chain []digest.Digest
		for i := range diffIDs {
			_ = identity.ChainID(chain) // parent layer chainID
			chain = append(chain, diffIDs[i])
			_ = identity.ChainID(chain).String() // current layer chainID
		}
		_ = identity.ChainID(chain).String()
	}

	numLayers := []int{5, 10, 25, 50}
	for _, sz := range numLayers {
		b.Run(fmt.Sprintf("num of layers: %d", sz), func(b *testing.B) {
			diffIDs := generateRandomDiffIDs(b, sz)
			for i := 0; i < b.N; i++ {
				unpackWithChainID(diffIDs)
			}
		})
	}
}

func BenchmarkUnpackWithChainIDs(b *testing.B) {
	// This simulates the new way of pre-calculating all chainIDs for every layer
	// by calling `identity.ChainIDs` once.
	unpackWithChainIDs := func(diffIDs []digest.Digest) {
		chainIDs := make([]digest.Digest, len(diffIDs))
		copy(chainIDs, diffIDs)
		chainIDs = identity.ChainIDs(chainIDs)
		for i := range diffIDs {
			if i > 0 {
				_ = chainIDs[i-1].String() // parent layer chainID
			}
			_ = chainIDs[i].String() // current layer chainID
		}
		if len(chainIDs) > 0 {
			_ = chainIDs[len(chainIDs)-1].String()
		}
	}

	numLayers := []int{5, 10, 25, 50}
	for _, sz := range numLayers {
		b.Run(fmt.Sprintf("num of layers: %d", sz), func(b *testing.B) {
			diffIDs := generateRandomDiffIDs(b, sz)
			for i := 0; i < b.N; i++ {
				unpackWithChainIDs(diffIDs)
			}
		})
	}
}

func TestBindToOverlay(t *testing.T) {
	testCases := []struct {
		name   string
		mounts []mount.Mount
		expect []mount.Mount
	}{
		{
			name: "single bind mount",
			mounts: []mount.Mount{
				{
					Type:    "bind",
					Source:  "/path/to/source",
					Options: []string{"ro", "rbind"},
				},
			},
			expect: []mount.Mount{
				{
					Type:   "overlay",
					Source: "overlay",
					Options: []string{
						"ro",
						"upperdir=/path/to/source",
					},
				},
			},
		},
		{
			name: "overlay mount",
			mounts: []mount.Mount{
				{
					Type:   "overlay",
					Source: "overlay",
					Options: []string{
						"lowerdir=/path/to/lower",
						"upperdir=/path/to/upper",
					},
				},
			},
			expect: []mount.Mount{
				{
					Type:   "overlay",
					Source: "overlay",
					Options: []string{
						"lowerdir=/path/to/lower",
						"upperdir=/path/to/upper",
					},
				},
			},
		},
		{
			name: "multiple mounts",
			mounts: []mount.Mount{
				{
					Type:   "bind",
					Source: "/path/to/source1",
				},
				{
					Type:   "bind",
					Source: "/path/to/source2",
				},
			},
			expect: []mount.Mount{
				{
					Type:   "bind",
					Source: "/path/to/source1",
				},
				{
					Type:   "bind",
					Source: "/path/to/source2",
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := bindToOverlay(tc.mounts)
			if !reflect.DeepEqual(result, tc.expect) {
				t.Errorf("unexpected result: got %v, want %v", result, tc.expect)
			}
		})
	}
}

func TestIsStaged(t *testing.T) {
	testCases := []struct {
		name   string
		mounts []mount.Mount
		expect bool
	}{
		{
			name:   "no mounts",
			mounts: nil,
			expect: false,
		},
		{
			name: "read-only mount",
			mounts: []mount.Mount{
				{Type: "erofs", Source: "/path/to/layer.erofs", Options: []string{"loop"}},
			},
			expect: true,
		},
		{
			name: "writable mount",
			mounts: []mount.Mount{
				{Type: "bind", Source: "/path", Options: []string{"rbind"}},
			},
			expect: false,
		},
		{
			name: "only the last mount is inspected",
			mounts: []mount.Mount{
				{Type: "bind", Source: "/lower", Options: []string{"rbind"}},
				{Type: "erofs", Source: "/path/to/layer.erofs", Options: []string{"loop"}},
			},
			expect: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expect, isStaged(tc.mounts))
		})
	}
}

// stagedSnapshotter reports every layer as staged (read-only mounts) and
// records the Prepare/Commit calls. Only Prepare and Commit are exercised on
// the staged path, so the embedded (nil) Snapshotter covers the rest of the
// interface.
type stagedSnapshotter struct {
	snapshots.Snapshotter
	prepares []call
	commits  []call
}

type call struct{ name, key, parent string }

func (s *stagedSnapshotter) Prepare(_ context.Context, key, parent string, _ ...snapshots.Opt) ([]mount.Mount, error) {
	s.prepares = append(s.prepares, call{key: key, parent: parent})
	return []mount.Mount{{Type: "erofs", Source: "/staged/layer.erofs", Options: []string{"ro"}}}, nil
}

func (s *stagedSnapshotter) Commit(_ context.Context, name, key string, opts ...snapshots.Opt) error {
	var info snapshots.Info
	for _, o := range opts {
		_ = o(&info)
	}
	s.commits = append(s.commits, call{name: name, key: key, parent: info.Parent})
	return nil
}

// failApplier fails the test if Apply is called.
type failApplier struct{ t *testing.T }

func (a failApplier) Apply(_ context.Context, desc ocispec.Descriptor, _ []mount.Mount, _ ...diff.ApplyOpt) (ocispec.Descriptor, error) {
	a.t.Errorf("Apply must not be called for layer %s", desc.Digest)
	return ocispec.Descriptor{}, nil
}

// TestUnpackStagedLayers verifies that when the snapshotter reports layers as
// staged (read-only mounts from Prepare) in parallel mode, the unpacker skips
// fetch+apply but still commits each layer, rebasing the real parent in at
// Commit time.
func TestUnpackStagedLayers(t *testing.T) {
	ctx := context.Background()

	diffIDs := generateRandomDiffIDs(t, 2)
	chainIDs := identity.ChainIDs(append([]digest.Digest{}, diffIDs...))
	layers := []ocispec.Descriptor{
		{MediaType: ocispec.MediaTypeImageLayerGzip, Digest: digest.FromString("layer-0"), Size: 1},
		{MediaType: ocispec.MediaTypeImageLayerGzip, Digest: digest.FromString("layer-1"), Size: 1},
	}

	cs := imagetest.NewContentStore(ctx, t)

	// Minimal image config carrying the layer diffIDs.
	config := cs.JSONObject(ocispec.MediaTypeImageConfig, struct {
		ocispec.Platform
		RootFS ocispec.RootFS `json:"rootfs"`
	}{
		Platform: ocispec.Platform{OS: "linux", Architecture: "amd64"},
		RootFS:   ocispec.RootFS{Type: "layers", DiffIDs: diffIDs},
	}).Descriptor

	sn := &stagedSnapshotter{}
	u, err := NewUnpacker(ctx, cs.Store,
		WithUnpackLimiter(semaphore.NewWeighted(4)),
		WithUnpackPlatform(Platform{
			Platform:                platforms.All,
			Snapshotter:             sn,
			Applier:                 failApplier{t},
			SnapshotterCapabilities: []string{snapshots.RebaseCap},
		}),
	)
	require.NoError(t, err)

	// A staged layer must never be fetched.
	fetch := images.HandlerFunc(func(_ context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
		t.Errorf("fetch must not happen for a staged layer (%s)", desc.Digest)
		return nil, nil
	})

	require.NoError(t, u.unpack(fetch, config, layers))

	// Parallel mode: Prepare gets no parent...
	require.Len(t, sn.prepares, 2)
	assert.Equal(t, "", sn.prepares[0].parent)
	assert.Equal(t, "", sn.prepares[1].parent)

	// ...and the parent is rebased in at Commit.
	require.Len(t, sn.commits, 2)
	assert.Equal(t, chainIDs[0].String(), sn.commits[0].name)
	assert.Equal(t, "", sn.commits[0].parent)
	assert.Equal(t, chainIDs[1].String(), sn.commits[1].name)
	assert.Equal(t, chainIDs[0].String(), sn.commits[1].parent)
}

// failPrepareSnapshotter stages every layer like stagedSnapshotter, except the
// Prepare of the layer at index failAt, which fails with a non-AlreadyExists
// error.
type failPrepareSnapshotter struct {
	stagedSnapshotter
	failAt int
	// prepareCalls counts every Prepare, including the failing one, which
	// stagedSnapshotter only records on success. Prepare is called from the
	// layer launch loop alone, so this needs no synchronization.
	prepareCalls int
}

var errPrepareFailed = errors.New("prepare failed")

func (s *failPrepareSnapshotter) Prepare(ctx context.Context, key, parent string, opts ...snapshots.Opt) ([]mount.Mount, error) {
	layer := s.prepareCalls
	s.prepareCalls++
	if layer == s.failAt {
		return nil, errPrepareFailed
	}
	return s.stagedSnapshotter.Prepare(ctx, key, parent, opts...)
}

// TestUnpackParallelPrepareError verifies that a topHalf failure in parallel
// mode is reported back from unpack instead of being silently dropped, while
// the layers queued before the failure are still committed.
func TestUnpackParallelPrepareError(t *testing.T) {
	ctx := context.Background()

	diffIDs := generateRandomDiffIDs(t, 3)
	chainIDs := identity.ChainIDs(append([]digest.Digest{}, diffIDs...))
	layers := []ocispec.Descriptor{
		{MediaType: ocispec.MediaTypeImageLayerGzip, Digest: digest.FromString("layer-0"), Size: 1},
		{MediaType: ocispec.MediaTypeImageLayerGzip, Digest: digest.FromString("layer-1"), Size: 1},
		{MediaType: ocispec.MediaTypeImageLayerGzip, Digest: digest.FromString("layer-2"), Size: 1},
	}

	cs := imagetest.NewContentStore(ctx, t)
	config := cs.JSONObject(ocispec.MediaTypeImageConfig, struct {
		ocispec.Platform
		RootFS ocispec.RootFS `json:"rootfs"`
	}{
		Platform: ocispec.Platform{OS: "linux", Architecture: "amd64"},
		RootFS:   ocispec.RootFS{Type: "layers", DiffIDs: diffIDs},
	}).Descriptor

	sn := &failPrepareSnapshotter{failAt: 1}
	u, err := NewUnpacker(ctx, cs.Store,
		WithUnpackLimiter(semaphore.NewWeighted(4)),
		WithUnpackPlatform(Platform{
			Platform:                platforms.All,
			Snapshotter:             sn,
			Applier:                 failApplier{t},
			SnapshotterCapabilities: []string{snapshots.RebaseCap},
		}),
	)
	require.NoError(t, err)

	fetch := images.HandlerFunc(func(_ context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
		t.Errorf("fetch must not happen for a staged layer (%s)", desc.Digest)
		return nil, nil
	})

	err = u.unpack(fetch, config, layers)
	require.ErrorIs(t, err, errPrepareFailed)

	// The launch loop stops at the failing layer: the third one is never
	// prepared, and only the first was prepared successfully.
	assert.Equal(t, 2, sn.prepareCalls)
	require.Len(t, sn.prepares, 1)

	// The layer prepared before the failure is still committed.
	require.Len(t, sn.commits, 1)
	assert.Equal(t, chainIDs[0].String(), sn.commits[0].name)
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
// config, the layers of every manifest are fetched but only the first
// manifest's layers are unpacked, and that a manifest without layers is
// ignored.
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
			wantFetched: slices.Concat([]ocispec.Descriptor{manifestA, manifestB, config}, layersB),
			wantUnpacks: 1,
		},
		{
			name:        "config between the manifests",
			children:    sharedConfig,
			order:       []ocispec.Descriptor{manifestA, config, manifestB, config},
			wantFetched: slices.Concat([]ocispec.Descriptor{manifestA, manifestB, config}, layersB),
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
