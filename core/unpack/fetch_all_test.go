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

package unpack_test

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/diff"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/images/imagetest"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/core/unpack"
	cerrdefs "github.com/containerd/errdefs"
	"github.com/containerd/platforms"
	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/identity"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnpackFetchesExistingSnapshots(t *testing.T) {
	t.Parallel()
	for _, fetchAll := range []bool{false, true} {
		for _, existing := range []int{0, 1, 2} {
			t.Run(fmt.Sprintf("fetchAll=%t/existing=%d", fetchAll, existing), func(t *testing.T) {
				t.Parallel()
				ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
				defer cancel()
				store, config, layers, blobs := unpackFixture(t, ctx)
				sn := &testSnapshotter{existing: make(map[string]bool)}
				chain := make([]digest.Digest, len(layers))
				for j, layer := range layers {
					chain[j] = layer.Digest
					if j < existing {
						sn.existing[identity.ChainID(chain[:j+1]).String()] = true
					}
				}
				secondFetch := make(chan struct{})
				firstApplied := make(chan struct{})
				var applied atomic.Int32
				applier := testApplier(func(ctx context.Context, desc ocispec.Descriptor) (ocispec.Descriptor, error) {
					if desc.Digest == layers[0].Digest {
						// The second download cannot finish until the first layer is
						// applied. A download-all-first implementation deadlocks here.
						select {
						case <-secondFetch:
						case <-ctx.Done():
							return ocispec.Descriptor{}, ctx.Err()
						}
						close(firstApplied)
					}
					b, err := content.ReadBlob(ctx, store.Store, desc)
					if err != nil {
						return ocispec.Descriptor{}, err
					}
					applied.Add(1)
					return ocispec.Descriptor{Digest: digest.FromBytes(b)}, nil
				})
				opts := []unpack.UnpackerOpt{unpack.WithUnpackPlatform(unpack.Platform{SnapshotterKey: "test", Snapshotter: sn, Applier: applier})}
				if fetchAll {
					opts = append(opts, unpack.WithFetchAllContent())
				}
				u, err := unpack.NewUnpacker(ctx, store.Store, opts...)
				require.NoError(t, err)
				h := images.HandlerFunc(func(ctx context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
					if desc.Digest == layers[1].Digest {
						close(secondFetch)
						if existing == 0 {
							select {
							case <-firstApplied:
							case <-ctx.Done():
								return nil, ctx.Err()
							}
						}
					}
					if b, ok := blobs[desc.Digest]; ok {
						return nil, content.WriteBlob(ctx, store.Store, desc.Digest.String(), bytes.NewReader(b), desc)
					}
					return images.Children(ctx, store.Store, desc)
				})
				manifest := store.JSONObject(ocispec.MediaTypeImageManifest, ocispec.Manifest{Config: config, Layers: layers}).Descriptor
				require.NoError(t, images.Dispatch(ctx, u.Unpack(h), nil, manifest))
				_, err = u.Wait()
				require.NoError(t, err)
				assert.Equal(t, int32(len(layers)-existing), applied.Load())
				for j, layer := range layers {
					b, err := content.ReadBlob(ctx, store.Store, layer)
					if !fetchAll && j < existing {
						assert.Truef(t, cerrdefs.IsNotFound(err), "default behavior should not fetch existing snapshots: %v", err)
						continue
					}
					require.NoError(t, err)
					assert.Equal(t, layer.Digest, digest.FromBytes(b))
				}
			})
		}
	}
}

func unpackFixture(t *testing.T, ctx context.Context) (imagetest.ContentStore, ocispec.Descriptor, []ocispec.Descriptor, map[digest.Digest][]byte) {
	t.Helper()
	store := imagetest.NewContentStore(ctx, t)
	var layers []ocispec.Descriptor
	var diffIDs []digest.Digest
	blobs := make(map[digest.Digest][]byte)
	for _, b := range [][]byte{[]byte("first layer"), []byte("second layer")} {
		d := digest.FromBytes(b)
		layers = append(layers, ocispec.Descriptor{MediaType: ocispec.MediaTypeImageLayer, Digest: d, Size: int64(len(b))})
		diffIDs = append(diffIDs, d)
		blobs[d] = b
	}
	config := store.JSONObject(ocispec.MediaTypeImageConfig, ocispec.Image{
		Platform: platforms.DefaultSpec(),
		RootFS:   ocispec.RootFS{Type: "layers", DiffIDs: diffIDs},
	}).Descriptor
	return store, config, layers, blobs
}

type testApplier func(context.Context, ocispec.Descriptor) (ocispec.Descriptor, error)

func (a testApplier) Apply(ctx context.Context, desc ocispec.Descriptor, _ []mount.Mount, _ ...diff.ApplyOpt) (ocispec.Descriptor, error) {
	return a(ctx, desc)
}

type testSnapshotter struct {
	snapshots.Snapshotter
	mu       sync.Mutex
	existing map[string]bool
}

func (s *testSnapshotter) Prepare(_ context.Context, _, _ string, opts ...snapshots.Opt) ([]mount.Mount, error) {
	var info snapshots.Info
	for _, opt := range opts {
		if err := opt(&info); err != nil {
			return nil, err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.existing[info.Labels[snapshots.LabelSnapshotRef]] {
		return nil, cerrdefs.ErrAlreadyExists
	}
	return nil, nil
}

func (s *testSnapshotter) Stat(_ context.Context, key string) (snapshots.Info, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.existing[key] {
		return snapshots.Info{Name: key}, nil
	}
	return snapshots.Info{}, cerrdefs.ErrNotFound
}

func (s *testSnapshotter) Commit(_ context.Context, name, _ string, _ ...snapshots.Opt) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.existing[name] = true
	return nil
}

func (s *testSnapshotter) Remove(context.Context, string) error {
	return nil
}
