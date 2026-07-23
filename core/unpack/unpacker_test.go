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
	"fmt"
	"reflect"
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
	"github.com/containerd/containerd/v2/core/snapshots"
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

// stagedSnapshotter reports every layer as staged (ErrAlreadyStaged) and records
// the Prepare/Commit calls. Only Prepare and Commit are exercised on the staged
// path, so the embedded (nil) Snapshotter covers the rest of the interface.
type stagedSnapshotter struct {
	snapshots.Snapshotter
	prepares []call
	commits  []call
}

type call struct{ name, key, parent string }

func (s *stagedSnapshotter) Prepare(_ context.Context, key, parent string, _ ...snapshots.Opt) ([]mount.Mount, error) {
	s.prepares = append(s.prepares, call{key: key, parent: parent})
	return nil, snapshots.ErrAlreadyStaged
}

func (s *stagedSnapshotter) Commit(_ context.Context, name, key string, opts ...snapshots.Opt) error {
	var info snapshots.Info
	for _, o := range opts {
		_ = o(&info)
	}
	s.commits = append(s.commits, call{name: name, key: key, parent: info.Parent})
	return nil
}

// failApplier fails the test if Apply is called; a staged layer is never applied.
type failApplier struct{ t *testing.T }

func (a failApplier) Apply(_ context.Context, desc ocispec.Descriptor, _ []mount.Mount, _ ...diff.ApplyOpt) (ocispec.Descriptor, error) {
	a.t.Errorf("Apply must not be called for a staged layer (%s)", desc.Digest)
	return ocispec.Descriptor{}, nil
}

// TestUnpackStagedLayers verifies that when the snapshotter reports layers as
// staged (ErrAlreadyStaged) in parallel mode, the unpacker skips fetch+apply but
// still commits each layer, rebasing the real parent in at Commit time.
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
