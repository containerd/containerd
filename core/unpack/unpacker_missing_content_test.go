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
	"sync"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/diff"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/errdefs"
	"github.com/containerd/platforms"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSnapshotExistsButContentMissing verifies that when a snapshot exists
// but content blob is missing, the unpacker fetches the missing content.
// See: https://github.com/containerd/containerd/issues/8973
func TestSnapshotExistsButContentMissing(t *testing.T) {
	ctx := context.Background()

	// Setup test data
	layerDigest := digest.FromString("test-layer")
	diffID := digest.FromString("test-diffid")
	config := ocispec.Image{RootFS: ocispec.RootFS{Type: "layers", DiffIDs: []digest.Digest{diffID}}}
	configBytes, _ := json.Marshal(config)
	configDigest := digest.FromBytes(configBytes)

	manifest := ocispec.Manifest{
		Config: ocispec.Descriptor{MediaType: ocispec.MediaTypeImageConfig, Digest: configDigest, Size: int64(len(configBytes))},
		Layers: []ocispec.Descriptor{{MediaType: ocispec.MediaTypeImageLayerGzip, Digest: layerDigest, Size: 1024}},
	}
	manifestBytes, _ := json.Marshal(manifest)

	// Track fetched content
	var fetchedDigests sync.Map

	// Content store: layer content initially missing
	cs := &mockContentStore{
		infoFunc: func(_ context.Context, dgst digest.Digest) (content.Info, error) {
			if dgst == configDigest {
				return content.Info{Digest: dgst}, nil
			}
			return content.Info{}, errdefs.ErrNotFound
		},
		readerAtFunc: func(_ context.Context, desc ocispec.Descriptor) (content.ReaderAt, error) {
			if desc.Digest == configDigest {
				return &mockReaderAt{data: configBytes}, nil
			}
			return nil, errdefs.ErrNotFound
		},
	}

	// Snapshotter: snapshot already exists
	sn := &mockSnapshotter{
		prepareFunc: func(context.Context, string, string, ...snapshots.Opt) ([]mount.Mount, error) {
			return nil, errdefs.ErrAlreadyExists
		},
		statFunc: func(_ context.Context, key string) (snapshots.Info, error) {
			if key == diffID.String() {
				return snapshots.Info{Name: key, Kind: snapshots.KindCommitted}, nil
			}
			return snapshots.Info{}, errdefs.ErrNotFound
		},
	}

	unpacker, err := NewUnpacker(ctx, cs, WithUnpackPlatform(Platform{
		Platform:       platforms.OnlyStrict(ocispec.Platform{OS: "linux", Architecture: "amd64"}),
		Snapshotter:    sn,
		SnapshotterKey: "overlayfs",
		Applier:        &mockApplier{},
	}))
	require.NoError(t, err)

	// Handler that tracks fetches
	fetchHandler := images.HandlerFunc(func(_ context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
		fetchedDigests.Store(desc.Digest, true)
		return nil, nil
	})
	childrenHandler := images.HandlerFunc(func(_ context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
		if desc.MediaType == ocispec.MediaTypeImageManifest {
			return []ocispec.Descriptor{manifest.Config, manifest.Layers[0]}, nil
		}
		return nil, nil
	})

	handler := unpacker.Unpack(images.Handlers(fetchHandler, childrenHandler))
	manifestDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(manifestBytes),
		Size:      int64(len(manifestBytes)),
	}

	_ = images.Dispatch(ctx, handler, nil, manifestDesc)
	time.Sleep(100 * time.Millisecond)

	// Verify layer was fetched even though snapshot existed
	_, fetched := fetchedDigests.Load(layerDigest)
	assert.True(t, fetched, "layer content should be fetched when snapshot exists but content is missing")
}

// Mock implementations

type mockContentStore struct {
	infoFunc     func(context.Context, digest.Digest) (content.Info, error)
	readerAtFunc func(context.Context, ocispec.Descriptor) (content.ReaderAt, error)
}

func (m *mockContentStore) Info(ctx context.Context, dgst digest.Digest) (content.Info, error) {
	if m.infoFunc != nil {
		return m.infoFunc(ctx, dgst)
	}
	return content.Info{}, errdefs.ErrNotFound
}
func (m *mockContentStore) ReaderAt(ctx context.Context, desc ocispec.Descriptor) (content.ReaderAt, error) {
	if m.readerAtFunc != nil {
		return m.readerAtFunc(ctx, desc)
	}
	return nil, errdefs.ErrNotFound
}
func (m *mockContentStore) Update(context.Context, content.Info, ...string) (content.Info, error) {
	return content.Info{}, nil
}
func (m *mockContentStore) Walk(context.Context, content.WalkFunc, ...string) error { return nil }
func (m *mockContentStore) Delete(context.Context, digest.Digest) error             { return nil }
func (m *mockContentStore) Status(context.Context, string) (content.Status, error) {
	return content.Status{}, nil
}
func (m *mockContentStore) ListStatuses(context.Context, ...string) ([]content.Status, error) {
	return nil, nil
}
func (m *mockContentStore) Abort(context.Context, string) error                          { return nil }
func (m *mockContentStore) Writer(context.Context, ...content.WriterOpt) (content.Writer, error) {
	return nil, nil
}

type mockSnapshotter struct {
	prepareFunc func(context.Context, string, string, ...snapshots.Opt) ([]mount.Mount, error)
	statFunc    func(context.Context, string) (snapshots.Info, error)
}

func (m *mockSnapshotter) Stat(ctx context.Context, key string) (snapshots.Info, error) {
	if m.statFunc != nil {
		return m.statFunc(ctx, key)
	}
	return snapshots.Info{}, errdefs.ErrNotFound
}
func (m *mockSnapshotter) Prepare(ctx context.Context, key, parent string, opts ...snapshots.Opt) ([]mount.Mount, error) {
	if m.prepareFunc != nil {
		return m.prepareFunc(ctx, key, parent, opts...)
	}
	return nil, nil
}
func (m *mockSnapshotter) Update(context.Context, snapshots.Info, ...string) (snapshots.Info, error) {
	return snapshots.Info{}, nil
}
func (m *mockSnapshotter) Usage(context.Context, string) (snapshots.Usage, error) {
	return snapshots.Usage{}, nil
}
func (m *mockSnapshotter) Mounts(context.Context, string) ([]mount.Mount, error)               { return nil, nil }
func (m *mockSnapshotter) Commit(context.Context, string, string, ...snapshots.Opt) error      { return nil }
func (m *mockSnapshotter) Remove(context.Context, string) error                                { return nil }
func (m *mockSnapshotter) View(context.Context, string, string, ...snapshots.Opt) ([]mount.Mount, error) {
	return nil, nil
}
func (m *mockSnapshotter) Walk(context.Context, snapshots.WalkFunc, ...string) error { return nil }
func (m *mockSnapshotter) Close() error                                              { return nil }

type mockReaderAt struct{ data []byte }

func (m *mockReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if off >= int64(len(m.data)) {
		return 0, nil
	}
	return copy(p, m.data[off:]), nil
}
func (m *mockReaderAt) Size() int64  { return int64(len(m.data)) }
func (m *mockReaderAt) Close() error { return nil }

type mockApplier struct{}

func (m *mockApplier) Apply(context.Context, ocispec.Descriptor, []mount.Mount, ...diff.ApplyOpt) (ocispec.Descriptor, error) {
	return ocispec.Descriptor{}, nil
}
