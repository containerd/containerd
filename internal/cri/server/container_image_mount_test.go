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

package server

import (
	"context"
	"strings"
	"testing"

	imagestore "github.com/containerd/containerd/v2/internal/cri/store/image"
	"github.com/containerd/containerd/v2/internal/cri/util"
	"github.com/containerd/errdefs"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// handlerPlatformImageService maps one runtime handler to a foreign platform,
// holds images by platform, and records the platform an image is resolved on.
// err fails every lookup on the foreign platform.
type handlerPlatformImageService struct {
	fakeImageService
	handler  string
	platform imagespec.Platform
	images   map[string]imagestore.Image
	err      error
	resolved []imagespec.Platform
}

func (s *handlerPlatformImageService) PlatformForImage(_, runtimeHandler string) imagespec.Platform {
	if runtimeHandler == s.handler {
		return s.platform
	}
	return util.NodePlatform()
}

func (s *handlerPlatformImageService) LocalResolve(_ string, platform imagespec.Platform) (imagestore.Image, error) {
	s.resolved = append(s.resolved, platform)
	if s.err != nil && !util.IsNodePlatform(platform) {
		return imagestore.Image{}, s.err
	}
	if img, ok := s.images[util.PlatformKey(platform)]; ok {
		return img, nil
	}
	return imagestore.Image{}, errdefs.ErrNotFound
}

var testForeignPlatform = imagespec.Platform{OS: "linux", Architecture: "riscv64"}

// TestImageVolumeResolvesOnTheHandlerPlatform pins that an image volume is
// resolved on the platform of the runtime handler of the sandbox, which is the
// platform kubelet pulled it for, like the image of the container.
func TestImageVolumeResolvesOnTheHandlerPlatform(t *testing.T) {
	images := &handlerPlatformImageService{handler: "runc-foreign", platform: testForeignPlatform}
	c := newTestCRIService()
	c.ImageService = images

	mount := &runtime.Mount{
		ContainerPath: "/volume",
		Readonly:      true,
		Image:         &runtime.ImageSpec{Image: "docker.io/library/busybox:latest"},
	}
	err := c.mutateImageMount(context.Background(), mount, "overlayfs", "sandbox", "runc-foreign")
	require.Error(t, err)
	require.NotEmpty(t, images.resolved)
	assert.Equal(t, testForeignPlatform, images.resolved[0])
}

// TestResolveImageForHandlerFallsBackToNode pins that an image missing on the
// platform of the runtime handler falls back to the platform of the node,
// which is what kubelet pulled for without the RuntimeClassInImageCriApi
// feature gate, and that nothing else falls back.
func TestResolveImageForHandlerFallsBackToNode(t *testing.T) {
	const ref = "docker.io/library/busybox:latest"
	nodeImage := imagestore.Image{ID: "sha256:" + strings.Repeat("1", 64)}
	foreignImage := imagestore.Image{ID: "sha256:" + strings.Repeat("2", 64)}
	node := util.NodePlatform()

	for _, tt := range []struct {
		desc         string
		images       map[string]imagestore.Image
		err          error
		wantImage    imagestore.Image
		wantPlatform imagespec.Platform
		wantErr      error
	}{
		{
			desc:         "pulled for the handler",
			images:       map[string]imagestore.Image{util.PlatformKey(testForeignPlatform): foreignImage, util.PlatformKey(node): nodeImage},
			wantImage:    foreignImage,
			wantPlatform: testForeignPlatform,
		},
		{
			desc:         "only pulled for the node",
			images:       map[string]imagestore.Image{util.PlatformKey(node): nodeImage},
			wantImage:    nodeImage,
			wantPlatform: node,
		},
		{
			desc:    "pulled for neither",
			wantErr: errdefs.ErrNotFound,
		},
		{
			desc:    "lookup fails for another reason",
			images:  map[string]imagestore.Image{util.PlatformKey(node): nodeImage},
			err:     errdefs.ErrUnavailable,
			wantErr: errdefs.ErrUnavailable,
		},
	} {
		t.Run(tt.desc, func(t *testing.T) {
			c := newTestCRIService()
			c.ImageService = &handlerPlatformImageService{handler: "runc-foreign", platform: testForeignPlatform, images: tt.images, err: tt.err}

			image, platform, err := c.resolveImageForHandler(context.Background(), ref, "runc-foreign")
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantImage.ID, image.ID)
			assert.Equal(t, tt.wantPlatform, platform)
		})
	}
}
