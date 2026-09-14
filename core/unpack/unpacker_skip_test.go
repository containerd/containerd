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
	"path/filepath"
	"testing"

	digest "github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/identity"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/semaphore"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/diff"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/images/imagetest"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/plugins/snapshots/native"
	"github.com/containerd/errdefs"
	"github.com/containerd/platforms"
)

// identityApplier is a diff.Applier stub whose Apply always reports the
// descriptor's own digest as the DiffID, regardless of mounts or content.
// It never writes anything to the provided mounts; it is only used to
// isolate core/unpack's snapshot/chain bookkeeping from any real diff
// application logic.
type identityApplier struct{}

func (identityApplier) Apply(_ context.Context, desc ocispec.Descriptor, _ []mount.Mount, _ ...diff.ApplyOpt) (ocispec.Descriptor, error) {
	return ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayer,
		Digest:    desc.Digest,
		Size:      desc.Size,
	}, nil
}

// testImage builds a synthetic single-manifest image with three layers -
// two ordinary layers with a skippable EROFS chunk-index layer (see
// images.IsSkippableLayerType) in between - in the given content store, and
// returns the manifest descriptor along with each component descriptor.
func testImage(tc imagetest.ContentStore) (manifestDesc, configDesc, layer0, layer1, layer2 ocispec.Descriptor) {
	layer0 = tc.Blob(ocispec.MediaTypeImageLayerGzip, []byte("layer-0-content")).Descriptor
	// A standalone chunk-index layer: its content is opaque to this test
	// (and, per the specification, to any consumer that doesn't implement
	// chunk indexes); it must not receive a snapshot.
	layer1 = tc.Blob(images.MediaTypeErofsChunkIndex, []byte("chunk-index-content")).Descriptor
	layer2 = tc.Blob(ocispec.MediaTypeImageLayerGzip, []byte("layer-2-content")).Descriptor

	configDesc = tc.JSONObject(ocispec.MediaTypeImageConfig, struct {
		ocispec.Platform
		RootFS ocispec.RootFS `json:"rootfs"`
	}{
		Platform: ocispec.Platform{OS: "linux", Architecture: "amd64"},
		RootFS: ocispec.RootFS{
			Type:    "layers",
			DiffIDs: []digest.Digest{layer0.Digest, layer1.Digest, layer2.Digest},
		},
	}).Descriptor

	manifestDesc = tc.Manifest(
		imagetest.Content{Descriptor: configDesc},
		imagetest.Content{Descriptor: layer0},
		imagetest.Content{Descriptor: layer1},
		imagetest.Content{Descriptor: layer2},
	).Descriptor
	return
}

// checkSkippedChunkIndexUnpack asserts the invariants that must hold after
// unpacking testImage's three layers, regardless of whether unpack ran
// sequentially or in parallel: no snapshot exists for the skipped middle
// layer, the third layer's snapshot is parented directly on the first
// layer's snapshot (hopping over the gap), and the final ChainID used for GC
// anchoring is computed over all three layers, including the skipped one.
func checkSkippedChunkIndexUnpack(ctx context.Context, t *testing.T, cs content.Store, sn snapshots.Snapshotter, snapshotterKey string, configDesc, layer0, layer1, layer2 ocispec.Descriptor) {
	t.Helper()

	diffIDs := []digest.Digest{layer0.Digest, layer1.Digest, layer2.Digest}
	chainIDs := identity.ChainIDs(diffIDs)

	_, err := sn.Stat(ctx, chainIDs[1].String())
	require.True(t, errdefs.IsNotFound(err), "expected no snapshot for the skipped layer, got: %v", err)

	info0, err := sn.Stat(ctx, chainIDs[0].String())
	require.NoError(t, err)
	require.Empty(t, info0.Parent)

	info2, err := sn.Stat(ctx, chainIDs[2].String())
	require.NoError(t, err)
	require.Equal(t, chainIDs[0].String(), info2.Parent)

	cinfo, err := cs.Info(ctx, configDesc.Digest)
	require.NoError(t, err)
	require.Equal(t, chainIDs[2].String(), cinfo.Labels["containerd.io/gc.ref.snapshot."+snapshotterKey])
}

// TestUnpackSkipsChunkIndexLayer verifies that a layer whose media type is
// skippable (currently only the EROFS image layer format specification's
// standalone chunk-index layer, see images.IsSkippableLayerType) gets no
// snapshot of its own during a sequential unpack, while the snapshot
// committed for the next real layer is parented directly on the last real
// layer's snapshot - and every layer, skipped or not, still occupies its
// own link in the DiffID/ChainID recursion, matching the specification's
// ChainID rules (https://github.com/erofs/erofs-image-spec, §5.3).
func TestUnpackSkipsChunkIndexLayer(t *testing.T) {
	ctx := context.Background()
	tc := imagetest.NewContentStore(ctx, t)

	sn, err := native.NewSnapshotter(filepath.Join(t.TempDir(), "snapshots"))
	require.NoError(t, err)
	t.Cleanup(func() { sn.Close() })

	manifestDesc, configDesc, layer0, layer1, layer2 := testImage(tc)

	u, err := NewUnpacker(ctx, tc.Store, WithUnpackPlatform(Platform{
		Platform:       platforms.All,
		SnapshotterKey: "native",
		Snapshotter:    sn,
		Applier:        identityApplier{},
	}))
	require.NoError(t, err)

	handler := u.Unpack(images.ChildrenHandler(tc.Store))
	require.NoError(t, images.Walk(ctx, handler, manifestDesc))

	result, err := u.Wait()
	require.NoError(t, err)
	require.Equal(t, 1, result.Unpacks)

	checkSkippedChunkIndexUnpack(ctx, t, tc.Store, sn, "native", configDesc, layer0, layer1, layer2)
}

// TestUnpackSkipsChunkIndexLayerParallel is TestUnpackSkipsChunkIndexLayer
// under parallel unpack (see Unpacker.supportParallel): snapshots are
// Prepared independently (without a parent) and only rebased onto their
// real parent at commit time via snapshots.WithParent. This exercises the
// parentChainID-based commit-time rebase path in core/unpack/unpacker.go,
// as opposed to the Prepare-time parent used sequentially.
func TestUnpackSkipsChunkIndexLayerParallel(t *testing.T) {
	ctx := context.Background()
	tc := imagetest.NewContentStore(ctx, t)

	sn, err := native.NewSnapshotter(filepath.Join(t.TempDir(), "snapshots"))
	require.NoError(t, err)
	t.Cleanup(func() { sn.Close() })

	manifestDesc, configDesc, layer0, layer1, layer2 := testImage(tc)

	u, err := NewUnpacker(ctx, tc.Store,
		WithUnpackLimiter(semaphore.NewWeighted(4)),
		WithUnpackPlatform(Platform{
			Platform:                platforms.All,
			SnapshotterKey:          "native",
			Snapshotter:             sn,
			SnapshotterCapabilities: []string{snapshots.RebaseCap},
			Applier:                 identityApplier{},
		}))
	require.NoError(t, err)

	handler := u.Unpack(images.ChildrenHandler(tc.Store))
	require.NoError(t, images.Walk(ctx, handler, manifestDesc))

	result, err := u.Wait()
	require.NoError(t, err)
	require.Equal(t, 1, result.Unpacks)

	checkSkippedChunkIndexUnpack(ctx, t, tc.Store, sn, "native", configDesc, layer0, layer1, layer2)
}
