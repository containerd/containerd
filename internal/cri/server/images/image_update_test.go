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
	"errors"
	"testing"

	"github.com/containerd/containerd/v2/core/snapshots"
	imagestore "github.com/containerd/containerd/v2/internal/cri/store/image"
	"github.com/containerd/containerd/v2/internal/cri/util"
	"github.com/containerd/errdefs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// unreachableSnapshotter fails every Stat with an error other than not found.
type unreachableSnapshotter struct {
	snapshots.Snapshotter
}

func (unreachableSnapshotter) Stat(context.Context, string) (snapshots.Info, error) {
	return snapshots.Info{}, errors.New("snapshotter unreachable")
}

// TestUpdateImageStoreDropsForeignImageNoLongerUnpacked pins that a reference
// stored for a foreign platform is dropped once its snapshot is gone, as it
// would not be stored on reload, and kept while the snapshotter cannot tell.
func TestUpdateImageStoreDropsForeignImageNoLongerUnpacked(t *testing.T) {
	ctx := context.Background()
	const tag = "docker.io/library/busybox:latest"

	records, blobs, nodeID, foreignID := newTwoPlatformImage(t, tag)

	c, _ := newTestCRIService()
	c.images = records
	c.imageStore = imagestore.NewStore(records, blobStore{blobs: blobs})
	c.runtimePlatforms["runc-foreign"] = ImagePlatform{Platform: testForeignPlatform, Snapshotter: "foreign"}
	c.imagePlatforms = imagePlatforms(c.runtimePlatforms)

	foreign, err := c.imageStore.Lookup(ctx, tag, testForeignPlatform)
	require.NoError(t, err)
	var sn snapshots.Snapshotter = fakeSnapshotter{present: map[string]bool{foreign.ChainID: true}}
	c.snapshotterProvider = func(string) snapshots.Snapshotter { return sn }

	require.NoError(t, c.updateImageStore(ctx, tag))
	id, err := c.imageStore.Resolve(tag, testForeignPlatform)
	require.NoError(t, err)
	require.Equal(t, foreignID, id)

	sn = unreachableSnapshotter{}
	require.NoError(t, c.updateImageStore(ctx, tag))
	id, err = c.imageStore.Resolve(tag, testForeignPlatform)
	require.NoError(t, err, "an unreachable snapshotter must not drop the image")
	assert.Equal(t, foreignID, id)

	sn = fakeSnapshotter{}
	require.NoError(t, c.updateImageStore(ctx, tag))
	_, err = c.imageStore.Resolve(tag, testForeignPlatform)
	assert.True(t, errdefs.IsNotFound(err), "an image no longer unpacked must be dropped from the foreign platform")

	id, err = c.imageStore.Resolve(tag, util.NodePlatform())
	require.NoError(t, err, "the platform of the node is not affected")
	assert.Equal(t, nodeID, id)
}
