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
	"testing"

	criconfig "github.com/containerd/containerd/v2/pkg/cri/config"
	imagestore "github.com/containerd/containerd/v2/pkg/cri/store/image"
	snapshotstore "github.com/containerd/containerd/v2/pkg/cri/store/snapshot"
	"github.com/containerd/containerd/v2/pkg/errdefs"
	"github.com/containerd/containerd/v2/platforms"
	"github.com/stretchr/testify/assert"
)

const (
	testImageFSPath = "/test/image/fs/path"
	// Use an image id as test sandbox image to avoid image name resolve.
	// TODO(random-liu): Change this to image name after we have complete image
	// management unit test framework.
	testSandboxImage = "sha256:c75bebcdd211f41b3a460c7bf82970ed6c75acaab9cd4c9a4e125b03ca113798" // #nosec G101
)

// newTestCRIService creates a fake criService for test.
func newTestCRIService() (*CRIImageService, *GRPCCRIImageService) {
	service := &CRIImageService{
		config:           testImageConfig,
		runtimePlatforms: map[string]ImagePlatform{},
		imageFSPaths:     map[string]string{"overlayfs": testImageFSPath},
		imageStore:       imagestore.NewStore(nil, nil, platforms.Default()),
		snapshotStore:    snapshotstore.NewStore(),
	}

	return service, &GRPCCRIImageService{service}
}

var testImageConfig = criconfig.ImageConfig{
	PinnedImages: map[string]string{
		"sandbox": testSandboxImage,
	},
}

func TestLocalResolve(t *testing.T) {
	image := imagestore.Image{
		ID:      "sha256:c75bebcdd211f41b3a460c7bf82970ed6c75acaab9cd4c9a4e125b03ca113799",
		ChainID: "test-chain-id-1",
		References: []string{
			"docker.io/library/busybox:latest",
			"docker.io/library/busybox@sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59582",
		},
		Size: 10,
	}
	c, _ := newTestCRIService()
	var err error
	c.imageStore, err = imagestore.NewFakeStore([]imagestore.Image{image})
	assert.NoError(t, err)

	for _, ref := range []string{
		"sha256:c75bebcdd211f41b3a460c7bf82970ed6c75acaab9cd4c9a4e125b03ca113799",
		"busybox",
		"busybox:latest",
		"busybox@sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59582",
		"library/busybox",
		"library/busybox:latest",
		"library/busybox@sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59582",
		"docker.io/busybox",
		"docker.io/busybox:latest",
		"docker.io/busybox@sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59582",
		"docker.io/library/busybox",
		"docker.io/library/busybox:latest",
		"docker.io/library/busybox@sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59582",
	} {
		img, err := c.LocalResolve(ref)
		assert.NoError(t, err)
		assert.Equal(t, image, img)
	}
	img, err := c.LocalResolve("randomid")
	assert.Equal(t, errdefs.IsNotFound(err), true)
	assert.Equal(t, imagestore.Image{}, img)
}

func TestRuntimeSnapshotter(t *testing.T) {
	defaultRuntime := criconfig.Runtime{
		Snapshotter: "",
	}

	fooRuntime := criconfig.Runtime{
		Snapshotter: "devmapper",
	}

	for _, test := range []struct {
		desc              string
		runtime           criconfig.Runtime
		expectSnapshotter string
	}{
		{
			desc:              "should return default snapshotter when runtime.Snapshotter is not set",
			runtime:           defaultRuntime,
			expectSnapshotter: criconfig.DefaultImageConfig().Snapshotter,
		},
		{
			desc:              "should return overridden snapshotter when runtime.Snapshotter is set",
			runtime:           fooRuntime,
			expectSnapshotter: "devmapper",
		},
	} {
		test := test
		t.Run(test.desc, func(t *testing.T) {
			cri, _ := newTestCRIService()
			cri.config = criconfig.DefaultImageConfig()
			assert.Equal(t, test.expectSnapshotter, cri.RuntimeSnapshotter(context.Background(), test.runtime))
		})
	}
}
