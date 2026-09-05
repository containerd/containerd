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

	"github.com/containerd/containerd/v2/core/snapshots"
	imagestore "github.com/containerd/containerd/v2/internal/cri/store/image"
	"github.com/containerd/containerd/v2/internal/cri/util"
	"github.com/containerd/errdefs"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// fakeSnapshotter reports a fixed set of snapshot keys as present.
type fakeSnapshotter struct {
	snapshots.Snapshotter
	present map[string]bool
}

func (f fakeSnapshotter) Stat(_ context.Context, key string) (snapshots.Info, error) {
	if f.present[key] {
		return snapshots.Info{Name: key}, nil
	}
	return snapshots.Info{}, fmt.Errorf("snapshot %q: %w", key, errdefs.ErrNotFound)
}

const (
	testChainID  = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
	testImageID  = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	testImageRef = "gcr.io/library/busybox:latest"
)

// withStoredImage puts an image in the CRI store for the given platform.
func withStoredImage(t *testing.T, c *CRIImageService, platform imagespec.Platform) imagestore.Image {
	t.Helper()
	img := imagestore.Image{
		ID:         testImageID,
		ChainID:    testChainID,
		References: []string{testImageRef},
		Platform:   platform,
	}
	store, err := imagestore.NewFakeStore([]imagestore.Image{img})
	require.NoError(t, err)
	c.imageStore = store
	return img
}

// TestImageStatusReportsAbsentWhenNotUnpacked covers what mikebrow and mxpv
// asked for: the same image can be unpacked in several snapshotters, and that
// changes outside of CRI, so presence is looked up rather than stored. An image
// that is not unpacked in the snapshotter of the handler is reported as absent,
// which makes the caller pull it through that snapshotter.
func TestImageStatusReportsAbsentWhenNotUnpacked(t *testing.T) {
	for _, tt := range []struct {
		desc           string
		unpackedIn     string
		runtimeHandler string
		wantPresent    bool
	}{
		{
			desc:        "present in the default snapshotter, no handler",
			unpackedIn:  "overlayfs",
			wantPresent: true,
		},
		{
			// Without a handler the question is whether the image is known,
			// not whether it can be run by a particular runtime. An image
			// pulled outside CRI is not unpacked and has to stay visible,
			// which integration covers in
			// TestContainerdSandboxImagePulledOutsideCRI.
			desc:        "not unpacked anywhere, no handler",
			unpackedIn:  "nowhere",
			wantPresent: true,
		},
		{
			desc:           "present in the snapshotter of the handler",
			unpackedIn:     "devmapper",
			runtimeHandler: "kata",
			wantPresent:    true,
		},
		{
			desc:           "unpacked only in the default snapshotter, asked for the handler",
			unpackedIn:     "overlayfs",
			runtimeHandler: "kata",
			wantPresent:    false,
		},
	} {
		t.Run(tt.desc, func(t *testing.T) {
			c, _ := newTestCRIService()
			c.config.Snapshotter = "overlayfs"
			c.runtimePlatforms["kata"] = ImagePlatform{Snapshotter: "devmapper"}
			withStoredImage(t, c, util.NodePlatform())
			c.snapshotterProvider = func(name string) snapshots.Snapshotter {
				return fakeSnapshotter{present: map[string]bool{testChainID: name == tt.unpackedIn}}
			}

			resp, err := c.ImageStatus(context.Background(), &runtime.ImageStatusRequest{
				Image: &runtime.ImageSpec{Image: testImageRef, RuntimeHandler: tt.runtimeHandler},
			})
			require.NoError(t, err)
			if !tt.wantPresent {
				assert.Nil(t, resp.GetImage(), "image must be reported as absent")
				return
			}
			require.NotNil(t, resp.GetImage())
			assert.Equal(t, testImageID, resp.GetImage().GetId())
			if tt.runtimeHandler != "" {
				assert.Equal(t, tt.runtimeHandler, resp.GetImage().GetSpec().GetRuntimeHandler(),
					"the status has to say which handler it is for")
			}
		})
	}
}

// TestImageStatusResolvesOnTheHandlerPlatform pins that the handler selects the
// platform the image is looked up on.
func TestImageStatusResolvesOnTheHandlerPlatform(t *testing.T) {
	c, _ := newTestCRIService()
	c.config.Snapshotter = "overlayfs"
	c.runtimePlatforms["runc-foreign"] = ImagePlatform{Platform: testForeignPlatform}
	c.snapshotterProvider = func(string) snapshots.Snapshotter {
		return fakeSnapshotter{present: map[string]bool{testChainID: true}}
	}

	// The image is only stored for the foreign platform.
	withStoredImage(t, c, testForeignPlatform)

	t.Run("found for the handler that names that platform", func(t *testing.T) {
		resp, err := c.ImageStatus(context.Background(), &runtime.ImageStatusRequest{
			Image: &runtime.ImageSpec{Image: testImageRef, RuntimeHandler: "runc-foreign"},
		})
		require.NoError(t, err)
		require.NotNil(t, resp.GetImage())
		assert.Equal(t, testImageID, resp.GetImage().GetId())
	})

	t.Run("absent on the platform of the node", func(t *testing.T) {
		resp, err := c.ImageStatus(context.Background(), &runtime.ImageStatusRequest{
			Image: &runtime.ImageSpec{Image: testImageRef},
		})
		require.NoError(t, err)
		assert.Nil(t, resp.GetImage())
	})
}

// TestImageStatusToleratesUnreachableSnapshotter pins that a snapshotter that
// cannot be resolved does not fail the request. Before this check existed the
// image was reported as present, and failing here would stall the caller.
func TestImageStatusToleratesUnreachableSnapshotter(t *testing.T) {
	c, _ := newTestCRIService()
	c.config.Snapshotter = "overlayfs"
	withStoredImage(t, c, util.NodePlatform())
	c.snapshotterProvider = func(string) snapshots.Snapshotter { return nil }

	resp, err := c.ImageStatus(context.Background(), &runtime.ImageStatusRequest{
		Image: &runtime.ImageSpec{Image: testImageRef},
	})
	require.NoError(t, err)
	require.NotNil(t, resp.GetImage())
	assert.Equal(t, testImageID, resp.GetImage().GetId())
}

func TestPlatformAndSnapshotterForRuntimeHandler(t *testing.T) {
	c, _ := newTestCRIService()
	c.config.Snapshotter = "overlayfs"
	c.runtimePlatforms["runc-foreign"] = ImagePlatform{Platform: testForeignPlatform}
	c.runtimePlatforms["kata"] = ImagePlatform{Snapshotter: "devmapper"}

	assert.True(t, util.IsNodePlatform(c.PlatformForRuntimeHandler("")))
	assert.True(t, util.IsNodePlatform(c.PlatformForRuntimeHandler("does-not-exist")))
	assert.True(t, util.IsNodePlatform(c.PlatformForRuntimeHandler("kata")))
	assert.Equal(t, util.PlatformKey(testForeignPlatform), util.PlatformKey(c.PlatformForRuntimeHandler("runc-foreign")))

	assert.Equal(t, "overlayfs", c.SnapshotterForRuntimeHandler(""))
	assert.Equal(t, "overlayfs", c.SnapshotterForRuntimeHandler("does-not-exist"))
	assert.Equal(t, "overlayfs", c.SnapshotterForRuntimeHandler("runc-foreign"))
	assert.Equal(t, "devmapper", c.SnapshotterForRuntimeHandler("kata"))

	// Sanity: the fixture must not be the platform of the node, or the
	// assertions above are vacuous.
	require.False(t, util.IsNodePlatform(testForeignPlatform))
}

// TestPinnedImageStaysOnNodePlatform pins that a pinned image is looked up on
// the platform of the node whatever handler asks about it, which is the platform
// PullImage pulls it for. Otherwise ImageStatus reports it absent right after a
// pull for that handler succeeded, and the caller pulls it again on every sync.
func TestPinnedImageStaysOnNodePlatform(t *testing.T) {
	c, _ := newTestCRIService()
	c.config.Snapshotter = "overlayfs"
	c.config.PinnedImages = map[string]string{"sandbox": testImageRef}
	c.runtimePlatforms["runc-foreign"] = ImagePlatform{Platform: testForeignPlatform}
	c.snapshotterProvider = func(string) snapshots.Snapshotter {
		return fakeSnapshotter{present: map[string]bool{testChainID: true}}
	}
	withStoredImage(t, c, util.NodePlatform())

	assert.True(t, util.IsNodePlatform(c.PlatformForImage(testImageRef, "runc-foreign")))
	assert.False(t, util.IsNodePlatform(c.PlatformForImage("docker.io/library/other:latest", "runc-foreign")))

	resp, err := c.ImageStatus(context.Background(), &runtime.ImageStatusRequest{
		Image: &runtime.ImageSpec{Image: testImageRef, RuntimeHandler: "runc-foreign"},
	})
	require.NoError(t, err)
	require.NotNil(t, resp.GetImage(), "a pinned image pulled for the node must be found for any handler")
	assert.Equal(t, testImageID, resp.GetImage().GetId())
}
