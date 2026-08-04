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
	"testing"

	"github.com/containerd/platforms"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"

	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/images/imagetest"
	"github.com/containerd/containerd/v2/core/remotes"
	"github.com/containerd/containerd/v2/core/remotes/docker"
	"github.com/containerd/containerd/v2/pkg/testutil"
)

// TestPullFetchesLayersOfEveryConfigSharingManifest runs the real pull
// pipeline (ChildrenHandler, FilterPlatforms, unpacker.Unpack, under
// images.Dispatch) against a local registry. The registry serves an index
// whose two same-platform manifests share one config digest but list
// different layer blobs. The layers of both must be fetched into the content
// store, or a later export or push of the image would fail with NotFound.
//
// The unpacker's platform matches nothing, so no snapshot is produced and no
// real snapshotter is needed. A real pull also unpacks the first set. Layer
// contents are arbitrary here, since they are fetched and never applied.
func TestPullFetchesLayersOfEveryConfigSharingManifest(t *testing.T) {
	ctx := context.Background()

	imagePlatform := ocispec.Platform{OS: "linux", Architecture: "amd64"}

	// One config, shared by both manifests. Both can share it because a config
	// records each layer's uncompressed digest (diffID), which compression does
	// not change.
	config := mustJSON(t, ocispec.Image{
		Platform: imagePlatform,
		RootFS:   ocispec.RootFS{Type: "layers", DiffIDs: []digest.Digest{digest.FromString("diff-0")}},
	})
	configDesc := blobDesc(ocispec.MediaTypeImageConfig, config)

	layerA := []byte("layer-a-compressed-bytes")
	layerB := []byte("layer-b-compressed-bytes")
	layerADesc := blobDesc(ocispec.MediaTypeImageLayerGzip, layerA)
	layerBDesc := blobDesc(ocispec.MediaTypeImageLayerGzip, layerB)

	manifestA := mustJSON(t, ocispec.Manifest{
		MediaType: ocispec.MediaTypeImageManifest,
		Config:    configDesc,
		Layers:    []ocispec.Descriptor{layerADesc},
	})
	manifestB := mustJSON(t, ocispec.Manifest{
		MediaType: ocispec.MediaTypeImageManifest,
		Config:    configDesc,
		Layers:    []ocispec.Descriptor{layerBDesc},
	})
	manifestADesc := blobDesc(ocispec.MediaTypeImageManifest, manifestA)
	manifestADesc.Platform = &imagePlatform
	manifestBDesc := blobDesc(ocispec.MediaTypeImageManifest, manifestB)
	manifestBDesc.Platform = &imagePlatform

	index := mustJSON(t, ocispec.Index{
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

	// NewResolver defaults to plain HTTP for localhost, where the registry
	// listens.
	resolver := docker.NewResolver(docker.ResolverOptions{})
	_, resolved, err := resolver.Resolve(ctx, ref)
	require.NoError(t, err)
	fetcher, err := resolver.Fetcher(ctx, ref)
	require.NoError(t, err)

	// Assemble the same handler chain a transfer-service pull uses.
	children := images.FilterPlatforms(images.ChildrenHandler(store), platforms.Only(imagePlatform))
	handler := images.Handlers(remotes.FetchHandler(store, fetcher), children)

	u, err := NewUnpacker(ctx, store, WithUnpackPlatform(Platform{
		Platform:    platforms.OnlyStrict(platforms.MustParse("linux/arm64")), // no match -> fetch only
		Snapshotter: stubSnapshotter{},
		Applier:     stubApplier{},
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

func mustJSON(t *testing.T, v any) []byte {
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
