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
	"context"
	"fmt"
	"testing"

	"github.com/containerd/containerd/v2/core/images"
	imagestore "github.com/containerd/containerd/v2/internal/cri/store/image"
	"github.com/containerd/containerd/v2/internal/cri/util"
	"github.com/containerd/errdefs"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// recordStore is an images.Store holding image records by name, enough for
// RemoveImage and for the CRI image store to resolve against.
type recordStore struct {
	images.Store
	records map[string]images.Image
}

func (s *recordStore) Get(_ context.Context, name string) (images.Image, error) {
	i, ok := s.records[name]
	if !ok {
		return images.Image{}, fmt.Errorf("image %q: %w", name, errdefs.ErrNotFound)
	}
	return i, nil
}

func (s *recordStore) Delete(_ context.Context, name string, _ ...images.DeleteOpt) error {
	if _, ok := s.records[name]; !ok {
		return fmt.Errorf("image %q: %w", name, errdefs.ErrNotFound)
	}
	delete(s.records, name)
	return nil
}

// TestRemoveImageRefreshesEveryPlatform pins that removing an image drops it
// from the CRI store whatever platform the request names. The containerd
// records RemoveImage deletes are shared by every platform a reference was
// pulled for, so refreshing only the platform of the request left the entry of
// the other platform behind until an image event happened to arrive.
func TestRemoveImageRefreshesEveryPlatform(t *testing.T) {
	ctx := context.Background()
	const tag = "docker.io/library/busybox:latest"

	// An index with both the platform of the node and the foreign platform
	// fully present, the state after the tag was pulled for both.
	blobs := map[digest.Digest][]byte{}
	var manifests []ocispec.Descriptor
	ids := map[string]string{}
	for _, p := range []ocispec.Platform{util.NodePlatform(), testForeignPlatform} {
		layer := []byte("layer-" + util.PlatformKey(p))
		layerDigest := digest.FromBytes(layer)
		blobs[layerDigest] = layer
		config := marshalBlob(t, blobs, ocispec.MediaTypeImageConfig, ocispec.Image{
			Platform: p,
			RootFS:   ocispec.RootFS{Type: "layers", DiffIDs: []digest.Digest{layerDigest}},
		}, nil, true)
		ids[util.PlatformKey(p)] = config.Digest.String()
		manifests = append(manifests, marshalBlob(t, blobs, ocispec.MediaTypeImageManifest, ocispec.Manifest{
			MediaType: ocispec.MediaTypeImageManifest,
			Config:    config,
			Layers:    []ocispec.Descriptor{{MediaType: ocispec.MediaTypeImageLayer, Digest: layerDigest, Size: int64(len(layer))}},
		}, &p, true))
	}
	index := marshalBlob(t, blobs, ocispec.MediaTypeImageIndex, ocispec.Index{
		MediaType: ocispec.MediaTypeImageIndex,
		Manifests: manifests,
	}, nil, true)
	nodeID, foreignID := ids[util.PlatformKey(util.NodePlatform())], ids[util.PlatformKey(testForeignPlatform)]
	require.NotEqual(t, nodeID, foreignID)

	records := &recordStore{records: map[string]images.Image{}}
	for _, name := range []string{tag, nodeID, foreignID} {
		records.records[name] = images.Image{Name: name, Target: index}
	}

	c, _ := newTestCRIService()
	c.images = records
	c.imageStore = imagestore.NewStore(records, blobStore{blobs: blobs})
	c.runtimePlatforms["runc-foreign"] = ImagePlatform{Platform: testForeignPlatform}
	c.imagePlatforms = imagePlatforms(c.runtimePlatforms)

	for _, ref := range []string{tag, nodeID} {
		require.NoError(t, c.imageStore.Update(ctx, ref, util.NodePlatform()))
	}
	for _, ref := range []string{tag, foreignID} {
		require.NoError(t, c.imageStore.Update(ctx, ref, testForeignPlatform))
	}
	require.Len(t, c.imageStore.List(), 2)

	// Remove the foreign image by id without a handler, which is how an image
	// listed by ListImages comes back to be removed.
	require.NoError(t, c.RemoveImage(ctx, &runtime.ImageSpec{Image: foreignID}))

	_, err := records.Get(ctx, foreignID)
	require.True(t, errdefs.IsNotFound(err), "the containerd record of the foreign id must be gone")
	_, err = c.imageStore.Get(foreignID)
	assert.True(t, errdefs.IsNotFound(err), "the foreign image must no longer be listed, got %+v", c.imageStore.List())

	// The tag record was shared with the node platform and is gone with it,
	// so the image of the node is only known by its id from here on.
	node, err := c.imageStore.Get(nodeID)
	require.NoError(t, err)
	assert.Equal(t, []string{nodeID}, node.References)
	assert.Len(t, c.imageStore.List(), 1)
}
